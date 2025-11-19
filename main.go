package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
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
	Port     = flag.String("port", "9443", "port listen to, seperated by ',' like: 80,443,1080 also can be range like 8080-8090, or combination of both ")
	Protocol = flag.String("protocol", "tcp", "by now 'tcp' is the only supported protocol")
	Host     = flag.String("host", "0.0.0.0", "host listen to")
	LogLevel = flag.String("log-level", "info", "logging level: [debug, info, warn, error]")
	help     = flag.Bool("help", false, "Display help message")
)

var serverPublicIP string
var dnsCache sync.Map

var ipv4Services = []string{
	"https://api.ipify.org",
	"https://ipv4.icanhazip.com",
	"https://ifconfig.me/ip",
	"https://checkip.amazonaws.com",
}

var ipv6Services = []string{
	"https://api6.ipify.org",
	"https://ipv6.icanhazip.com",
	"https://ifconfig.me/ip",
	"https://ident.me",
}

func main() {
	flag.Parse()

	if *help {
		flag.Usage()
		os.Exit(0)
	}

	switch *LogLevel {
	case "debug":
		slog.SetLogLoggerLevel(slog.LevelDebug)
	case "info":
		slog.SetLogLoggerLevel(slog.LevelInfo)
	case "warn":
		slog.SetLogLoggerLevel(slog.LevelWarn)
	case "error":
		slog.SetLogLoggerLevel(slog.LevelError)
	default:
		log.Fatalf("Invalid log level: %s", *LogLevel)
	}

	serverPublicIP = detectPublicIP()

	ports := slices.DeleteFunc(strings.Split(*Port, ","), func(e string) bool {
		return e == ""
	})
	protocols := slices.DeleteFunc(strings.Split(*Protocol, ","), func(e string) bool {
		return e == ""
	})

	for _, protocol := range protocols {
		if !slices.Contains([]string{"tcp"}, protocol) {
			slog.Error("defined protocol is not correct, please check your input! (only tcp is supported)")
			os.Exit(1)
		}

		for _, port := range ports {
			if strings.Contains(port, "-") {
				pRange := slices.DeleteFunc(strings.Split(port, "-"), func(e string) bool {
					return e == ""
				})

				if len(pRange) != 2 {
					slog.Error("defined port range is not correct, please check your input!", "range", port)
					os.Exit(1)
				}

				if !isValidPort(pRange[0]) || !isValidPort(pRange[1]) {
					slog.Error("defined port range is not correct, please check your input!", "range", port)
					os.Exit(1)
				}

				start, _ := strconv.Atoi(pRange[0])
				end, _ := strconv.Atoi(pRange[1])

				if start > end {
					slog.Error("defined port range start is greater than end", "start", start, "end", end)
					os.Exit(1)
				}

				for i := start; i <= end; i++ {
					go listenOn(protocol, *Host, strconv.Itoa(i))
				}
			} else {
				if !isValidPort(port) {
					slog.Error("defined port is not correct, please check your input!", "port", port)
					os.Exit(1)
				}
				go listenOn(protocol, *Host, port)
			}
		}
	}

	// Block forever while listeners run in goroutines.
	select {}
}

func isValidPort(inp string) bool {
	v, err := strconv.Atoi(inp)
	if err != nil {
		return false
	}
	return v > 0 && v <= 65535
}

func listenOn(protocol string, host string, port string) {
	address := host + ":" + port
	ln, err := net.Listen(protocol, address)
	if err != nil {
		slog.Error("Error listening", "addr", protocol+"://"+address, "details", err)
		return
	}
	defer ln.Close()

	slog.Info("listening on", "addr", protocol+"://"+address)

	for {
		conn, err := ln.Accept()
		if err != nil {
			slog.Error("Error accepting connection", "details", err)
			continue
		}
		go handleConnection(conn, port)
	}
}

func handleConnection(clientConn net.Conn, incomingPort string) {
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

	destPort = calculateBackendPort(incomingPort)

	// Initial detection timeout
	if err := clientConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		slog.Error("context timeout", "details", err)
		return
	}

	isHttps, clientReader1, err = isHTTPS(clientConn)
	if err != nil {
		slog.Error("couldn't determine if it's http or https", "details", err)
		return
	}

	// Clear deadline after detection
	if err := clientConn.SetReadDeadline(time.Time{}); err != nil {
		slog.Error("clearing read deadline", "details", err)
		return
	}

	if isHttps {
		slog.Debug("its https!")

		clientHello, clientReader2, err = peekClientHello(clientReader1)
		if err != nil {
			slog.Error("reading clientHello", "details", err)
			return
		}
		sni = clientHello.ServerName
	} else {
		slog.Debug("its http!")

		sni, clientReader2, err = readRequestURLHttp(clientReader1)
		if err != nil {
			slog.Error("reading hostname from http", "details", err)
			return
		}
	}

	if sni == "" {
		slog.Error("could not determine hostname (empty SNI/Host), closing connection")
		return
	}

	slog.Debug("Got new request", "host", sni, "port", destPort)

	backendAddr := net.JoinHostPort(sni, destPort)

	if isLocalIP(sni) {
		slog.Error("Loop blocked: request host points to local container", "host", sni)
		return
	}

	if isProxyItself(sni, destPort) {
		slog.Error("Blocking request to prevent proxy loop",
			"host", sni,
			"port", destPort,
		)
		return
	}

	if resolvesToSelf(sni) {
		slog.Error("Blocked: Host resolves to this server's public IP",
			"host", sni, "public_ip", serverPublicIP)
		return
	}

	backendConn, err := net.DialTimeout("tcp", backendAddr, 5*time.Second)
	if err != nil {
		slog.Error("failed to connect to backend", "addr", backendAddr, "details", err)
		return
	}
	defer backendConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	// Backend -> Client
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, backendConn)
		if c, ok := clientConn.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		} else {
			_ = clientConn.Close()
		}
	}()

	// Client -> Backend (using buffered reader that still has peeked bytes)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(backendConn, clientReader2)
		if c, ok := backendConn.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		} else {
			_ = backendConn.Close()
		}
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
			// TLS handshake record
			return true, originalConn, nil
		}
		return false, originalConn, nil
	}

	return false, originalConn, fmt.Errorf("no data read from the connection")
}

func readRequestURLHttp(conn io.Reader) (string, io.Reader, error) {
	peekedBytes := new(bytes.Buffer)
	reader := bufio.NewReader(io.TeeReader(conn, peekedBytes))

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

	// Use the fixed ReadOnlyConn (pointer receiver) and set a handshake timeout
	conn := &ReadOnlyConn{reader: reader}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	err := tls.Server(conn, &tls.Config{
		GetConfigForClient: func(argHello *tls.ClientHelloInfo) (*tls.Config, error) {
			h := new(tls.ClientHelloInfo)
			*h = *argHello
			hello = h
			// We return nil config, nil error – handshake will later fail,
			// but by then we've already captured ClientHello.
			return nil, nil
		},
	}).Handshake()

	if hello == nil {
		return nil, err
	}

	return hello, nil
}

func getSelfIPs() []net.IP {
	var result []net.IP

	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				ip := ipnet.IP
				if ip.IsLoopback() {
					continue
				}
				result = append(result, ip)
			}
		}
	}

	return result
}

func isProxyItself(targetHost, targetPort string) bool {
	ips, err := net.LookupIP(targetHost)
	if err != nil {
		return false
	}

	myIPs := getSelfIPs()

	for _, targetIP := range ips {
		for _, myIP := range myIPs {
			if targetIP.Equal(myIP) {
				slog.Warn("Loop detected: backend resolves to this proxy",
					"resolved_ip", targetIP.String(),
					"my_ip", myIP.String(),
					"port", targetPort,
				)
				return true
			}
		}
	}

	return false
}

func isLocalIP(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	if ip.IsLoopback() {
		return true
	}

	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if ip.Equal(ipnet.IP) {
					return true
				}
			}
		}
	}

	return false
}

func calculateBackendPort(incoming string) string {
	p, err := strconv.Atoi(incoming)
	if err != nil {
		return incoming
	}

	if p >= 9000 && p <= 9999 {
		return strconv.Itoa(p - 9000)
	}

	return incoming
}

func isValidIPv4(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.To4() != nil
}

func isValidIPv6(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.To4() == nil
}

func detectPublicIP() string {
	client := http.Client{Timeout: 4 * time.Second}

	for _, service := range ipv4Services {
		slog.Info("Trying IPv4 public IP service", "url", service)

		resp, err := client.Get(service)
		if err != nil {
			slog.Warn("IPv4 service failed", "url", service, "err", err)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		ip := strings.TrimSpace(string(body))

		if isValidIPv4(ip) {
			slog.Info("Detected public IPv4", "ip", ip, "service", service)
			return ip
		}
	}

	slog.Warn("No IPv4 detected – falling back to IPv6")

	for _, service := range ipv6Services {
		slog.Info("Trying IPv6 public IP service", "url", service)

		resp, err := client.Get(service)
		if err != nil {
			slog.Warn("IPv6 service failed", "url", service, "err", err)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		ip := strings.TrimSpace(string(body))

		if isValidIPv6(ip) {
			slog.Info("Detected public IPv6", "ip", ip, "service", service)
			return ip
		}
	}

	slog.Error("Failed to detect public IP via all providers")
	return ""
}

func resolvesToSelf(host string) bool {
	if host == serverPublicIP {
		return true
	}

	if cached, ok := dnsCache.Load(host); ok {
		ips := cached.([]net.IP)
		for _, ip := range ips {
			if ip.String() == serverPublicIP {
				return true
			}
		}
		return false
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return false
	}

	dnsCache.Store(host, ips)

	for _, ip := range ips {
		if ip.String() == serverPublicIP {
			return true
		}
	}

	return false
}
