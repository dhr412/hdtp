# HDTP: Hybrid Data Transfer Protocol

HDTP is a PoC for a network protocol built on UDP, designed to combine TCP's reliability with UDP's speed. It supports both unicast and multicast communication, ensuring guaranteed and ordered delivery with error detection, suitable for scenarios requiring fast and reliable data dissemination.

## What It Is

HDTP is an experimental protocol implemented in Go, leveraging UDP to provide:

- **Guaranteed Delivery**: Ensures all packets reach recipients via ACK/NACK-based retransmissions.
- **Ordered Delivery**: Uses sequence numbers for in-order packet processing.
- **Error Detection**: Employs SHA-256 checksums to verify packet integrity, with retransmissions on mismatches.
- **Multicast Support**: Enables one-to-many communication using UDP multicast.
- **Throughput Control**: Limits sending rate with a token bucket (10 packets/second).
- **Stateless Design**: Maintains no persistent connection state, with self-contained packets.

The PoC supports both IPv4 and IPv6 multicast and is designed to outperform TCP in speed while being more reliable than raw UDP.

## The Need

TCP offers reliability but incurs high latency due to connection setup and congestion control, making it suboptimal for real-time or multicast applications. UDP is fast and supports multicast but lacks reliability, risking data loss. HDTP addresses this gap by providing a lightweight, reliable protocol for both unicast and multicast scenarios, ideal for applications like real-time data streaming, IoT updates, or distributed systems requiring low latency and high reliability.

## How It Works

- **Sender**: Splits messages into chunks (≤1000 bytes), assigns sequence numbers, and computes SHA-256 checksums. Packets are sent to a multicast or unicast UDP address, with a token bucket limiting throughput. The sender listens for ACK/NACK responses on a separate port (8319) and retransmits (up to 5 retries) on timeouts (1-second) or NACKs. An end packet signals transmission completion.
- **Receiver**: Joins a multicast group or listens on a unicast address (port 8316), verifies packet checksums, and sends ACKs for valid packets or NACKs for corrupted ones. It buffers packets and delivers them in order based on sequence numbers, printing received messages.
- **Execution**: Run with `go run hdtp.go --mode sender --message "msg1,msg2"` for sending or `go run hdtp.go --mode receiver` for receiving, with an optional `--ipv6` flag for IPv6 support.

## License

This project is licensed under the MIT License.

