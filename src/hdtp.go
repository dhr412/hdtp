package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"time"
)

const (
	TypeData uint8 = 0
	TypeACK  uint8 = 1
	TypeNACK uint8 = 2
)

type Packet struct {
	Type     uint8
	SeqNum   uint32
	Payload  []byte
	Checksum [32]byte
}

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

func Sender(multicastAddr, localAddr string, messages []string) {
	addr, err := net.ResolveUDPAddr("udp", multicastAddr)
	if err != nil {
		log.Fatalf("ResolveUDPAddr failed: %v", err)
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		log.Fatalf("DialUDP failed: %v", err)
	}
	defer conn.Close()

	listener, err := net.ListenUDP("udp", &net.UDPAddr{Port: 8316})
	if err != nil {
		log.Fatalf("ListenUDP failed: %v", err)
	}
	defer listener.Close()

	tb := NewTokenBucket(10, 100*time.Millisecond)

	pending := make(map[uint32]Packet)
	ackChan := make(chan struct {
		Seq   uint32
		IsACK bool
	})

	go func() {
		buf := make([]byte, 5)
		for {
			n, _, err := listener.ReadFromUDP(buf)
			if err != nil {
				log.Printf("ACK/NACK read error: %v", err)
				continue
			}
			if n == 5 {
				packetType := buf[0]
				seqNum := binary.BigEndian.Uint32(buf[1:5])
				isACK := (packetType == TypeACK)
				ackChan <- struct {
					Seq   uint32
					IsACK bool
				}{Seq: seqNum, IsACK: isACK}
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
			checksum := sha256.Sum256(payload)
			packet := Packet{Type: TypeData, SeqNum: seqNum, Payload: payload, Checksum: checksum}

			buf := make([]byte, 1+4+32+len(payload))
			buf[0] = packet.Type
			binary.BigEndian.PutUint32(buf[1:5], packet.SeqNum)
			copy(buf[5:37], packet.Checksum[:])
			copy(buf[37:], packet.Payload)

			_, err := conn.Write(buf)
			if err != nil {
				log.Printf("Write failed: %v", err)
				continue
			}
			pending[seqNum] = packet

			go func(seq uint32, buf []byte) {
				for {
					timeout := time.NewTimer(1 * time.Second)
					select {
					case ack := <-ackChan:
						if ack.Seq == seq {
							if ack.IsACK {
								delete(pending, seq)
								return
							} else {
								_, err := conn.Write(buf)
								if err != nil {
									log.Printf("Retransmit failed: %v", err)
								}
							}
						}
					case <-timeout.C:
						if _, ok := pending[seq]; ok {
							_, err := conn.Write(buf)
							if err != nil {
								log.Printf("Retransmit failed: %v", err)
							}
						} else {
							return
						}
					}
					timeout.Stop()
				}
			}(seqNum, buf)

			seqNum++
		}
	}
}

func Receiver(multicastAddr, localAddr string) {
	addr, err := net.ResolveUDPAddr("udp", multicastAddr)
	if err != nil {
		log.Fatalf("ResolveUDPAddr failed: %v", err)
	}
	conn, err := net.ListenMulticastUDP("udp", nil, addr)
	if err != nil {
		log.Fatalf("ListenMulticastUDP failed: %v", err)
	}
	defer conn.Close()

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
		if packetType != TypeData {
			continue
		}

		seqNum := binary.BigEndian.Uint32(buf[1:5])
		if n < 37 {
			continue
		}
		checksum := [32]byte{}
		copy(checksum[:], buf[5:37])
		payload := buf[37:n]

		calcChecksum := sha256.Sum256(payload)
		checksumMatch := calcChecksum == checksum

		respConn, err := net.DialUDP("udp", nil, srcAddr)
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

			received[seqNum] = payload

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
			log.Printf("Checksum mismatch for seq %d", seqNum)
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
	multicastAddr := "239.255.0.1:8316"
	localAddr := "0.0.0.0:8316"

	go Sender(multicastAddr, localAddr, []string{"Hello, multicast!", "Second message", "Third message"})
	Receiver(multicastAddr, localAddr)
}
