## usage
```bash
$ ./forward_proxy -h

Usage of /forward_proxy:
  -help
        Display help message
  -host string
        host listen to (default "0.0.0.0")
  -port string
        port listen to, seperated by ',' like: 80,443,1080 also can be range like 8080-8090, or combination of both  (default "8080")
  -protocol string
        by now 'tcp' is the only supported protocol (default "tcp")
```

## To-Do:
- [x] http tcp
- [x] https tcp
- [x] multiple ports
- [ ] http udp
- [ ] https udp