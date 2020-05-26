package main

import (
	"bytes"
	"crypto/tls"
	"encoding/gob"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

func serve(app *config) {

	if app.tls && !fileExists(app.tlsKey) {
		log.Printf("key file not found: %s - disabling TLS", app.tlsKey)
		app.tls = false
	}

	if app.tls && !fileExists(app.tlsCert) {
		log.Printf("cert file not found: %s - disabling TLS", app.tlsCert)
		app.tls = false
	}

	var wg sync.WaitGroup

	for _, h := range app.listeners {
		hh := appendPortIfMissing(h, app.defaultPort)
		//listenTCP(app, &wg, hh)
		listenUDP(app, &wg, hh)
	}

	wg.Wait()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func listenTCP(app *config, wg *sync.WaitGroup, h string) {
	log.Printf("listenTCP: TLS=%v spawning TCP listener: %s", app.tls, h)

	// first try TLS
	if app.tls {
		listener, errTLS := listenTLS(app, h)
		if errTLS == nil {
			spawnAcceptLoopTCP(app, wg, listener, true)
			return
		}
		log.Printf("listenTLS: %v", errTLS)
		// TLS failed, try plain TCP
	}

	listener, errListen := net.Listen("tcp", h)
	if errListen != nil {
		log.Printf("listenTCP: TLS=%v %s: %v", app.tls, h, errListen)
		return
	}
	spawnAcceptLoopTCP(app, wg, listener, false)
}

func spawnAcceptLoopTCP(app *config, wg *sync.WaitGroup, listener net.Listener, isTLS bool) {
	wg.Add(1)
	go handleTCP(app, wg, listener, isTLS)
}

func listenTLS(app *config, h string) (net.Listener, error) {
	cert, errCert := tls.LoadX509KeyPair(app.tlsCert, app.tlsKey)
	if errCert != nil {
		log.Printf("listenTLS: failure loading TLS key pair: %v", errCert)
		app.tls = false // disable TLS
		return nil, errCert
	}

	config := &tls.Config{Certificates: []tls.Certificate{cert}}
	listener, errListen := tls.Listen("tcp", h, config)
	return listener, errListen
}

func listenUDP(app *config, wg *sync.WaitGroup, h string) {
	log.Printf("serve: spawning UDP listener: %s", h)

	udpAddr, errAddr := net.ResolveUDPAddr("udp", h)
	if errAddr != nil {
		log.Printf("listenUDP: bad address: %s: %v", h, errAddr)
		return
	}

	conn, errListen := net.ListenUDP("udp", udpAddr)
	if errListen != nil {
		log.Printf("net.ListenUDP: %s: %v", h, errListen)
		return
	}

	wg.Add(2)
	go runUDPTimer(app, wg)
	go handleUDP(app, wg, conn)
}

func appendPortIfMissing(host, port string) string {

LOOP:
	for i := len(host) - 1; i >= 0; i-- {
		c := host[i]
		switch c {
		case ']':
			break LOOP
		case ':':
			/*
				if i == len(host)-1 {
					return host[:len(host)-1] + port // drop repeated :
				}
			*/
			return host
		}
	}

	return host + port
}

func handleTCP(app *config, wg *sync.WaitGroup, listener net.Listener, isTLS bool) {
	defer wg.Done()

	var id int

	var aggReader aggregate
	var aggWriter aggregate

	for {
		conn, errAccept := listener.Accept()
		if errAccept != nil {
			log.Printf("handle: accept: %v", errAccept)
			break
		}
		go handleConnection(conn, id, 0, isTLS, &aggReader, &aggWriter)
		id++
	}
}

type udpInfo struct {
	remote *net.UDPAddr
	opt    options
	acc    *account
	start  time.Time
	id     int
}

var idleCount int32

type myAccount struct {
	recvCount int64
	recvBytes int64
}

var printSignal = []byte("@*@")

func runUDPTimer(app *config, wg *sync.WaitGroup) {
	defer wg.Done()

	serverAddr := "127.0.0.1" + app.defaultPort
	conn, err := net.Dial("udp", serverAddr)
	if err != nil {
		log.Printf("runUDPTimer: net.Dial %s error: %v", serverAddr, err)
		return
	}
	defer conn.Close()

	for {
		time.Sleep(2000 * time.Millisecond)

		if atomic.AddInt32(&idleCount, 1) == 2 {
			_, err = conn.Write(printSignal)
			if err != nil {
				log.Printf("runUDPTimer: conn.Write error: %v", err)
				continue
			}
		}
	}

}

func handleUDP(app *config, wg *sync.WaitGroup, conn *net.UDPConn) {
	defer wg.Done()

	buf := make([]byte, app.opt.ReadSize)

	ac := myAccount{}
	last := myAccount{}

	for {
		n, src, errRead := conn.ReadFromUDP(buf)
		if src == nil {
			log.Printf("handleUDP: read nil src: error: %v", errRead)
			continue
		}
		if n == len(printSignal) && bytes.Compare(buf[:n], printSignal) == 0 {
			rc := ac.recvCount - last.recvCount
			rb := ac.recvBytes - last.recvBytes
			var bp int64
			if rc > 0 {
				bp = rb / rc
			}
			log.Printf(" received pkts: %8v -> %8v = %8v | received packet: %8v -> %8v = %8v | b/p = %v",
				last.recvCount, ac.recvCount, rc,
				last.recvBytes, ac.recvBytes, rb,
				bp)
			/*
				log.Printf(" received bytes: %v -> %v, + %v. received packet: %v -> %v, + %v",
					last.recvBytes, ac.recvBytes, ac.recvBytes-last.recvBytes,
					last.recvCount, ac.recvCount, ac.recvCount-last.recvCount)
			*/
			last = ac
			continue
		}

		ac.recvCount++
		ac.recvBytes += int64(n)
		atomic.StoreInt32(&idleCount, 0)
	}
}

func handleConnection(conn net.Conn, c, connections int, isTLS bool, aggReader, aggWriter *aggregate) {
	defer conn.Close()

	log.Printf("handleConnection: incoming: %s %v", protoLabel(isTLS), conn.RemoteAddr())

	// receive options
	var opt options
	dec := gob.NewDecoder(conn)
	if errOpt := dec.Decode(&opt); errOpt != nil {
		log.Printf("handleConnection: options failure: %v", errOpt)
		return
	}
	log.Printf("handleConnection: options received: %v", opt)

	// send ack
	a := newAck()
	if errAck := ackSend(false, conn, a); errAck != nil {
		log.Printf("handleConnection: sending ack: %v", errAck)
		return
	}

	go serverReader(conn, opt, c, connections, isTLS, aggReader)

	if !opt.PassiveServer {
		go serverWriter(conn, opt, c, connections, isTLS, aggWriter)
	}

	tickerPeriod := time.NewTimer(opt.TotalDuration)

	<-tickerPeriod.C
	log.Printf("handleConnection: %v timer", opt.TotalDuration)

	tickerPeriod.Stop()

	log.Printf("handleConnection: closing: %v", conn.RemoteAddr())
}

func serverReader(conn net.Conn, opt options, c, connections int, isTLS bool, agg *aggregate) {

	log.Printf("serverReader: starting: %s %v", protoLabel(isTLS), conn.RemoteAddr())

	connIndex := fmt.Sprintf("%d/%d", c, connections)

	buf := make([]byte, opt.ReadSize)

	workLoop(connIndex, "serverReader", "rcv/s", conn.Read, buf, opt.ReportInterval, 0, nil, agg)

	log.Printf("serverReader: exiting: %v", conn.RemoteAddr())
}

func protoLabel(isTLS bool) string {
	if isTLS {
		return "TLS"
	}
	return "TCP"
}

func serverWriter(conn net.Conn, opt options, c, connections int, isTLS bool, agg *aggregate) {

	log.Printf("serverWriter: starting: %s %v", protoLabel(isTLS), conn.RemoteAddr())

	connIndex := fmt.Sprintf("%d/%d", c, connections)

	buf := randBuf(opt.WriteSize)

	workLoop(connIndex, "serverWriter", "snd/s", conn.Write, buf, opt.ReportInterval, opt.MaxSpeed, nil, agg)

	log.Printf("serverWriter: exiting: %v", conn.RemoteAddr())
}

func serverWriterTo(conn *net.UDPConn, opt options, dst net.Addr, acc *account, c, connections int, agg *aggregate) {
	log.Printf("serverWriterTo: starting: UDP %v", dst)

	start := acc.prevTime

	udpWriteTo := func(b []byte) (int, error) {
		if time.Since(start) > opt.TotalDuration {
			return -1, fmt.Errorf("udpWriteTo: total duration %s timer", opt.TotalDuration)
		}

		return conn.WriteTo(b, dst)
	}

	connIndex := fmt.Sprintf("%d/%d", c, connections)

	buf := randBuf(opt.WriteSize)

	workLoop(connIndex, "serverWriterTo", "snd/s", udpWriteTo, buf, opt.ReportInterval, opt.MaxSpeed, nil, agg)

	log.Printf("serverWriterTo: exiting: %v", dst)
}
