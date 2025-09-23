package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

const (
	MulticastPort = 8316
	ACKPort       = 8319

	TypeData uint8 = 0
	TypeACK  uint8 = 1
	TypeNACK uint8 = 2
	TypeEnd  uint8 = 3
)

type Packet struct {
	Type    uint8
	SeqNum  uint32
	Payload []byte
	Nonce   [12]byte
}

var srtt time.Duration = 200 * time.Millisecond

type TokenBucket struct {
	tokens     int
	capacity   int
	rate       time.Duration
	lastRefill time.Time
}

func NewTokenBucket(capacity int, rate time.Duration) *TokenBucket {
	return &TokenBucket{
		tokens:     capacity,
		capacity:   capacity,
		rate:       rate,
		lastRefill: time.Now(),
	}
}

func (tb *TokenBucket) Take() bool {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill)
	tokensToAdd := int(elapsed / tb.rate)
	if tokensToAdd > 0 {
		tb.tokens = min(tb.capacity, tb.tokens+tokensToAdd)
		tb.lastRefill = now
	}
	if tb.tokens > 0 {
		tb.tokens--
		return true
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func Sender(multicastAddr, localAddr string, messages []string, encryptionKey []byte, isIPv6 bool) {
	addr, err := net.ResolveUDPAddr("udp", multicastAddr)
	if err != nil {
		log.Fatalf("ResolveUDPAddr failed: %v", err)
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		log.Fatalf("DialUDP failed: %v", err)
	}
	defer conn.Close()

	laddr := &net.UDPAddr{Port: ACKPort}
	if isIPv6 {
		laddr.IP = net.IPv6zero
	}
	listener, err := net.ListenUDP("udp", laddr)
	if err != nil {
		log.Fatalf("ListenUDP failed: %v", err)
	}
	defer listener.Close()

	sendTimes := make(map[uint32]time.Time)
	tb := NewTokenBucket(10, 100*time.Millisecond)
	pending := make(map[uint32]Packet)
	ackChannels := make(map[uint32]chan bool)
	var ackMutex sync.RWMutex
	var wg sync.WaitGroup

	// ACK listener goroutine
	go func() {
		buf := make([]byte, 5)
		for {
			n, _, err := listener.ReadFromUDP(buf)
			if err != nil {
				if strings.Contains(err.Error(), "use of closed network connection") {
					return
				}
				log.Printf("ACK/NACK read error: %v", err)
				continue
			}
			if n == 5 {
				packetType := buf[0]
				seqNum := binary.BigEndian.Uint32(buf[1:5])
				isACK := (packetType == TypeACK)

				ackMutex.RLock()
				if ch, exists := ackChannels[seqNum]; exists {
					select {
					case ch <- isACK:
					default:
					}
				}
				ackMutex.RUnlock()
			}
		}
	}()

	seqNum := uint32(0)
	for _, msg := range messages {
		for _, chunk := range splitMessage(msg, 1000) {
			for !tb.Take() {
				time.Sleep(10 * time.Millisecond)
			}

			payload := []byte(chunk)

			block, err := aes.NewCipher(encryptionKey)
			if err != nil {
				log.Fatalf("AES cipher creation failed: %v", err)
			}
			gcm, err := cipher.NewGCM(block)
			if err != nil {
				log.Fatalf("GCM creation failed: %v", err)
			}
			nonce := make([]byte, gcm.NonceSize())
			if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
				log.Fatalf("Nonce generation failed: %v", err)
			}
			encrypted := gcm.Seal(nil, nonce, payload, nil) // ciphertext + tag

			packet := Packet{Type: TypeData, SeqNum: seqNum, Nonce: [12]byte(nonce), Payload: encrypted}

			buf := make([]byte, 1+4+12+len(encrypted))
			buf[0] = packet.Type
			binary.BigEndian.PutUint32(buf[1:5], packet.SeqNum)
			copy(buf[5:17], packet.Nonce[:])
			copy(buf[17:], packet.Payload)

			sendTimes[seqNum] = time.Now()
			_, err = conn.Write(buf)
			if err != nil {
				log.Printf("Write failed: %v", err)
				continue
			}
			pending[seqNum] = packet

			ackCh := make(chan bool, 1)
			ackMutex.Lock()
			ackChannels[seqNum] = ackCh
			ackMutex.Unlock()

			wg.Add(1)
			go func(seq uint32, buf []byte, ackCh chan bool) {
				defer wg.Done()
				defer func() {
					ackMutex.Lock()
					delete(ackChannels, seq)
					ackMutex.Unlock()
					close(ackCh)
				}()

				maxRetries := 5
				retries := 0
				for retries < maxRetries {
					timeout := time.NewTimer(1 * time.Second)
					select {
					case isACK := <-ackCh:
						timeout.Stop()
						if isACK {
							rtt := time.Since(sendTimes[seq])
							srtt = (7*srtt + rtt) / 8
							tb.rate = time.Duration(0.5*float64(srtt.Milliseconds())+50) * time.Millisecond
							delete(sendTimes, seq)
							delete(pending, seq)
							return
						} else {
							_, err := conn.Write(buf)
							if err != nil {
								log.Printf("Retransmit failed for seq %d: %v", seq, err)
							}
							retries++
						}
					case <-timeout.C:
						if _, ok := pending[seq]; ok {
							_, err := conn.Write(buf)
							if err != nil {
								log.Printf("Retransmit failed for seq %d: %v", seq, err)
							}
							retries++
						} else {
							return
						}
					}
				}
				log.Printf("Max retries reached for seq %d, giving up", seq)
				delete(pending, seq)
			}(seqNum, buf, ackCh)
			seqNum++
		}
	}

	wg.Wait()

	endBuf := make([]byte, 5)
	endBuf[0] = TypeEnd
	binary.BigEndian.PutUint32(endBuf[1:5], seqNum)
	_, err = conn.Write(endBuf)
	if err != nil {
		log.Printf("End packet write failed: %v", err)
	}
}

func Receiver(multicastAddr, localAddr string, encryptionKey []byte, isIPv6 bool) {
	addr, err := net.ResolveUDPAddr("udp", multicastAddr)
	if err != nil {
		log.Fatalf("ResolveUDPAddr failed: %v", err)
	}

	laddr := &net.UDPAddr{Port: MulticastPort}
	if isIPv6 {
		laddr.IP = net.IPv6zero
	} else {
		laddr.IP = net.IPv4zero
	}
	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		log.Fatalf("ListenUDP failed: %v", err)
	}
	defer conn.Close()

	var pc interface {
		JoinGroup(ifi *net.Interface, group net.Addr) error
		SetMulticastLoopback(bool) error
	}
	if isIPv6 {
		pc = ipv6.NewPacketConn(conn)
	} else {
		pc = ipv4.NewPacketConn(conn)
	}
	if err := pc.JoinGroup(nil, addr); err != nil {
		log.Fatalf("JoinGroup failed: %v", err)
	}
	if err := pc.SetMulticastLoopback(true); err != nil {
		log.Fatalf("SetMulticastLoopback failed: %v", err)
	}

	received := make(map[uint32][]byte)
	expectedSeq := uint32(0)

	for {
		buf := make([]byte, 65535)
		n, srcAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("Read failed: %v", err)
			continue
		}

		if n < 5 {
			continue
		}

		packetType := buf[0]
		if packetType == TypeEnd {
			log.Println("Received end-of-transmission packet, exiting")
			return
		}
		if packetType != TypeData {
			continue
		}

		seqNum := binary.BigEndian.Uint32(buf[1:5])
		if n < 17 {
			continue
		}
		nonce := [12]byte{}
		copy(nonce[:], buf[5:17])
		encrypted := buf[17:n]

		block, err := aes.NewCipher(encryptionKey)
		if err != nil {
			log.Fatalf("AES cipher creation failed: %v", err)
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			log.Fatalf("GCM creation failed: %v", err)
		}
		payload, err := gcm.Open(nil, nonce[:], encrypted, nil)
		checksumMatch := (err == nil)
		ackAddr := &net.UDPAddr{
			IP:   srcAddr.IP,
			Port: ACKPort,
		}
		respConn, err := net.DialUDP("udp", nil, ackAddr)
		if err != nil {
			log.Printf("Dial for response failed: %v", err)
			continue
		}

		respBuf := make([]byte, 5)
		binary.BigEndian.PutUint32(respBuf[1:5], seqNum)

		if checksumMatch {
			respBuf[0] = TypeACK
			_, err = respConn.Write(respBuf)
			if err != nil {
				log.Printf("ACK write failed: %v", err)
			}
			respConn.Close()

			received[seqNum] = payload // Use decrypted payload

			for {
				if payload, ok := received[expectedSeq]; ok {
					fmt.Printf("Received ordered: %s\n", string(payload))
					delete(received, expectedSeq)
					expectedSeq++
				} else {
					break
				}
			}
		} else {
			log.Printf("Decryption/verification failed for seq %d: %v", seqNum, err)
			respBuf[0] = TypeNACK
			_, err = respConn.Write(respBuf)
			if err != nil {
				log.Printf("NACK write failed: %v", err)
			}
			respConn.Close()
		}
	}
}

func splitMessage(msg string, size int) []string {
	var chunks []string
	for i := 0; i < len(msg); i += size {
		end := i + size
		if end > len(msg) {
			end = len(msg)
		}
		chunks = append(chunks, msg[i:end])
	}
	return chunks
}

func main() {
	mode := flag.String("mode", "", "Mode: 'sender (s)' or 'receiver (r)'")
	message := flag.String("msg", "Hello,World,This is a test message", "Comma-separated messages to send (sender mode only)")
	useIPv6 := flag.Bool("ipv6", false, "Use IPv6 multicast")
	encKey := flag.String("key", "", "Optional 32-byte AES-256 key (defaults to built-in)")
	flag.Parse()

	var encryptionKey []byte
	if *encKey != "" {
		if len(*encKey) != 32 {
			log.Fatalf("Invalid key length: must be exactly 32 bytes for AES-256")
		}
		encryptionKey = []byte(*encKey)
	} else {
		encryptionKey = []byte("32-byte-key-for-AES-256-for-tls!")
	}

	var multicastAddr, localAddr string
	if *useIPv6 {
		multicastAddr = fmt.Sprintf("[ff02::1]:%d", MulticastPort)
		localAddr = fmt.Sprintf("[::]:%d", MulticastPort)
	} else {
		multicastAddr = fmt.Sprintf("239.255.0.1:%d", MulticastPort)
		localAddr = fmt.Sprintf("0.0.0.0:%d", MulticastPort)
	}

	switch *mode {
	case "sender", "s":
		messages := strings.Split(*message, ",")
		log.Printf("Starting sender with %d messages", len(messages))
		Sender(multicastAddr, localAddr, messages, encryptionKey, *useIPv6)
	case "receiver", "r":
		log.Println("Starting receiver")
		Receiver(multicastAddr, localAddr, encryptionKey, *useIPv6)
	default:
		fmt.Println("Usage:")
		fmt.Printf("  Sender: %s --mode sender --message \"Hello,World,Test\"\n", os.Args[0])
		fmt.Printf("  Receiver: %s --mode receiver\n", os.Args[0])
		os.Exit(1)
	}
}
