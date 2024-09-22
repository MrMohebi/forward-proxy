package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	Port     = flag.String("port", "8080", "port listen to, seperated by ',' like: 80,443,1080 also can be range like 8080-8090, or combination of both ")
	Protocol = flag.String("protocol", "tcp", "by now 'tcp' is the only supported protocol")
	Host     = flag.String("host", "0.0.0.0", "host listen to")
	help     = flag.Bool("help", false, "Display help message")
)

func main() {
	flag.Parse()

	if *help {
		flag.Usage()
		os.Exit(0)
	}

	ports := slices.DeleteFunc(strings.Split(*Port, ","), func(e string) bool {
		return e == ""
	})
	protocols := slices.DeleteFunc(strings.Split(*Protocol, ","), func(e string) bool {
		return e == ""
	})

	var wg sync.WaitGroup

	for _, protocol := range protocols {
		if !slices.Contains([]string{"tcp"}, protocol) {
			log.Fatal("defined protocol in not correct, please check your input! (only support tcp)")
		}

		for _, port := range ports {
			if strings.Contains(port, "-") {
				pRange := slices.DeleteFunc(strings.Split(port, "-"), func(e string) bool {
					return e == ""
				})
				if !isNumber(pRange[0]) || !isNumber(pRange[1]) {
					log.Fatal("defined port in not correct, please check your input!")
				}
				start, _ := strconv.Atoi(pRange[0])
				end, _ := strconv.Atoi(pRange[1])

				for i := start; i <= end; i++ {
					wg.Add(1)
					go listenOn(protocol, *Host+":"+strconv.Itoa(i))
				}
			} else {
				if !isNumber(port) {
					log.Fatal("defined port in not correct, please check your input!")
				}
				wg.Add(1)
				go listenOn(protocol, *Host+":"+port)
			}

		}
	}

	wg.Wait()
}

func listenOn(protocol string, address string) {
	ln, err := net.Listen(protocol, address)
	if err != nil {
		fmt.Println("Error listening:", err)
		return
	}
	defer ln.Close()

	fmt.Println("listening on:", protocol+"://"+address)
	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Println("Error accepting connection:", err)
			continue
		}
		go handleConnection(conn)
	}
}

func isNumber(inp string) bool {
	if _, err := strconv.Atoi(inp); err == nil {
		return true
	}
	return false
}

func handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	var (
		clientReader1 io.Reader
		clientReader2 io.Reader
		err           error
		isHttps       bool
		sni           string
		clientHello   *tls.ClientHelloInfo
		destPort      string
	)

	if err := clientConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		log.Print(err)
		return
	}

	isHttps, clientReader1, err = isHTTPS(clientConn)
	if err != nil {
		println(err)
	}

	if err := clientConn.SetReadDeadline(time.Time{}); err != nil {
		log.Print(err)
		return
	}

	if isHttps {
		println("its https!")

		clientHello, clientReader2, err = peekClientHello(clientReader1)
		if err != nil {
			log.Print(err)
			return
		}
		sni = clientHello.ServerName
		destPort = "443"
	} else {
		println("its http!")

		sni, clientReader2, err = readRequestURLHttp(clientReader1)
		if err != nil {
			log.Print(err)
			return
		}
		destPort = "80"
	}

	println(sni)

	backendConn, err := net.DialTimeout("tcp", net.JoinHostPort(sni, destPort), 5*time.Second)
	if err != nil {
		log.Print("err in sending req")
		log.Print(err)
		return
	}
	defer backendConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		io.Copy(clientConn, backendConn)
		clientConn.(*net.TCPConn).CloseWrite()
		wg.Done()
	}()
	go func() {
		io.Copy(backendConn, clientReader2)
		backendConn.(*net.TCPConn).CloseWrite()
		wg.Done()
	}()

	wg.Wait()
}

func isHTTPS(conn net.Conn) (bool, io.Reader, error) {
	peekedBytes := new(bytes.Buffer)
	buf := make([]byte, 1)
	n, err := io.TeeReader(conn, peekedBytes).Read(buf)
	originalConn := io.MultiReader(peekedBytes, conn)
	if err != nil {
		return false, originalConn, fmt.Errorf("error reading from connection: %w", err)
	}

	if n > 0 {
		if buf[0] == 0x16 {
			return true, originalConn, nil // It's HTTPS
		}
		return false, originalConn, nil // It's HTTP
	}

	return false, originalConn, fmt.Errorf("no data read from the connection")
}

func readRequestURLHttp(conn io.Reader) (string, io.Reader, error) {
	peekedBytes := new(bytes.Buffer)
	reader := bufio.NewReader(io.TeeReader(conn, peekedBytes))

	// Parse the HTTP request from the reader
	req, err := http.ReadRequest(reader)
	originalConn := io.MultiReader(peekedBytes, conn)
	if err != nil {
		return "", originalConn, fmt.Errorf("failed to read request: %w", err)
	}
	return getHost(req), originalConn, nil
}
func getHost(r *http.Request) string {
	host := r.Host
	if i := strings.Index(host, ":"); i != -1 {
		host = host[:i]
	}
	return host
}

func peekClientHello(reader io.Reader) (*tls.ClientHelloInfo, io.Reader, error) {
	peekedBytes := new(bytes.Buffer)
	hello, err := readClientHello(io.TeeReader(reader, peekedBytes))
	if err != nil {
		return nil, nil, err
	}
	return hello, io.MultiReader(peekedBytes, reader), nil
}

func readClientHello(reader io.Reader) (*tls.ClientHelloInfo, error) {
	var hello *tls.ClientHelloInfo

	err := tls.Server(ReadOnlyConn{reader: reader}, &tls.Config{
		GetConfigForClient: func(argHello *tls.ClientHelloInfo) (*tls.Config, error) {
			hello = new(tls.ClientHelloInfo)
			*hello = *argHello
			return nil, nil
		},
	}).Handshake()

	if hello == nil {
		return nil, err
	}

	return hello, nil
}
