# HDTP: Hybrid Data Transfer Protocol

HDTP is a Proof of Concept for a network protocol built on UDP, designed to combine TCP's reliability with UDP's speed. It supports both unicast and multicast communication, ensuring guaranteed and ordered delivery with error detection and encryption, suitable for scenarios requiring fast and reliable data dissemination.

## What It Is

HDTP is an experimental protocol implemented in Go, leveraging UDP to provide:

- **Guaranteed Delivery**: Ensures all packets reach recipients via ACK/NACK-based retransmissions.
- **Ordered Delivery**: Uses sequence numbers for in-order packet processing.
- **Error Detection and Encryption**: Employs AES-256-GCM for encryption and integrity verification, with retransmissions on decryption failures.
- **Multicast Support**: Enables one-to-many communication using UDP multicast when the `--multicast` flag is set.
- **Throughput Control**: Limits sending rate with a token bucket (10 packets/second, adaptive based on smoothed RTT).
- **Stateless Design**: Maintains no persistent connection state, with self-contained packets.

The PoC supports both IPv4 and IPv6 multicast and is designed to outperform TCP in speed while being more reliable than raw UDP.

## The Need

TCP offers reliability but incurs high latency due to connection setup and congestion control, making it suboptimal for real-time or multicast applications. UDP is fast and supports multicast but lacks reliability, risking data loss. HDTP addresses this gap by providing a lightweight, reliable, and encrypted protocol for both unicast and multicast scenarios, ideal for applications like real-time data streaming, IoT updates, or distributed systems requiring low latency and high reliability.

## How It Works

- **Sender**: Splits messages into chunks (≤1000 bytes), assigns sequence numbers, and encrypts payloads using AES-256-GCM with a 32-byte key. Packets are sent to a multicast or unicast UDP address, with a token bucket limiting throughput. The sender listens for ACK/NACK responses on port 8319 and retransmits (up to 5 retries) on timeouts (1-second) or NACKs. An end packet signals transmission completion.
- **Receiver**: Joins a multicast group (if `--multicast` is set) or listens on a unicast address (port 8316), decrypts and verifies packets using AES-256-GCM, and sends ACKs for valid packets or NACKs for corrupted ones. It buffers packets and delivers them in order based on sequence numbers, printing received messages.
- **Adaptive Rate Control**: The sender adjusts the token bucket rate based on smoothed round-trip time (SRTT), starting at 200ms and updated as (7*SRTT + RTT)/8.

## Usage

### Running the Executable from Releases

Download the precompiled binary for your platform from the [Releases](https://github.com/dhr412/hdtp/releases) page.

#### Sender

To send messages, run:

```bash
./hdtp --mode sender [--multicast] [--target "address:port"] --msg "msg1,msg2,msg3" [--ipv6] [--key "32-byte-key-for-AES-256-for-tls!"]
```

- `--mode sender` or `-mode s`: Sets sender mode.
- `--multicast`: Enables multicast mode (default: unicast).
- `--target`: Target address:port for unicast mode (default: `127.0.0.1:8316`).
- `--msg`: Comma-separated messages to send (default: "Hello,World,This is a test message").
- `--ipv6`: Enables IPv6 multicast (default: IPv4).
- `--key`: Optional 32-byte AES-256 key (default: built-in key).

Example (unicast):

```bash
./hdtp -mode s -msg "Hello,World,Test" -target "127.0.0.1:8316"
```

Example (multicast):

```bash
./hdtp -mode s -msg "Hello,World,Test" --multicast
```

#### Receiver

To receive messages, run:

```bash
./hdtp --mode receiver [--multicast] [--target "address:port"] [--ipv6] [--key "32-byte-key-for-AES-256-for-tls!"]
```

- `--mode receiver` or `-mode r`: Sets receiver mode.
- `--multicast`: Enables multicast mode (default: unicast).
- `--target`: Target address:port for unicast mode (default: `127.0.0.1:8316`).
- `--ipv6`: Enables IPv6 multicast (default: IPv4).
- `--key`: Optional 32-byte AES-256 key (must match sender's key).

Example (unicast):

```bash
./hdtp -mode r -target "127.0.0.1:8316"
```

Example (multicast):

```bash
./hdtp -mode r --multicast
```

### Running from Source Code

Ensure Go 1.20 or later is installed and network access is available for UDP multicast (IPv4: `239.255.0.1:8316`, IPv6: `[ff02::1]:8316`).

#### Sender

To send messages, compile and run:

```bash
go run hdtp.go --mode sender [--multicast] [--target "address:port"] --msg "msg1,msg2,msg3" [--ipv6] [--key "32-byte-key-for-AES-256-for-tls!"]
```

- `--mode sender` or `-mode s`: Sets sender mode.
- `--multicast`: Enables multicast mode (default: unicast).
- `--target`: Target address:port for unicast mode (default: `127.0.0.1:8316`).
- `--msg`: Comma-separated messages to send (default: "Hello,World,This is a test message").
- `--ipv6`: Enables IPv6 multicast (default: IPv4).
- `--key`: Optional 32-byte AES-256 key (default: built-in key).

Example (unicast):

```bash
go run hdtp.go -mode s -msg "Hello,World,Test" -target "127.0.0.1:8316"
```

Example (multicast):

```bash
go run hdtp.go -mode s -msg "Hello,World,Test" --multicast
```

#### Receiver

To receive messages, compile and run:

```bash
go run hdtp.go --mode receiver [--multicast] [--target "address:port"] [--ipv6] [--key "32-byte-key-for-AES-256-for-tls!"]
```

- `--mode receiver` or `-mode r`: Sets receiver mode.
- `--multicast`: Enables multicast mode (default: unicast).
- `--target`: Target address:port for unicast mode (default: `127.0.0.1:8316`).
- `--ipv6`: Enables IPv6 multicast (default: IPv4).
- `--key`: Optional 32-byte AES-256 key (must match sender's key).

Example (unicast):

```bash
go run hdtp.go -mode r -target "127.0.0.1:8316"
```

Example (multicast):

```bash
go run hdtp.go -mode r --multicast
```

### Notes

- Ensure the sender and receiver use the same encryption key, IP version, and multicast setting.
- For multicast, use `--multicast` on both sender and receiver; for unicast, specify `--target` or use the default.
- Run the receiver before the sender to avoid missing packets.
- The program exits after receiving an end-of-transmission packet (receiver) or sending all messages (sender).

## License

This project is licensed under the MIT License.
