package mbserver

import (
	"errors"
	"io"
	"log"
	"time"

	"github.com/goburrow/serial"
)

// ListenRTU starts the Modbus server listening to a serial device.
// For example:  err := s.ListenRTU(&serial.Config{Address: "/dev/ttyUSB0"}, 15)
func (s *Server) ListenRTU(serialConfig *serial.Config, address uint8) (err error) {
	port, err := serial.Open(serialConfig)
	if err != nil {
		log.Fatalf("failed to open %s: %v\n", serialConfig.Address, err)
	}

	// flush the read buffer on startup
	_, err = port.Read(make([]byte, 512))
	if err != nil {
		if err != serial.ErrTimeout {
			log.Fatalf("failed to flush read buffer for %s: %v\n", serialConfig.Address, err)
		}
		err = nil
	}
	s.ports = append(s.ports, port)

	s.portsWG.Add(1)
	go func() {
		defer s.portsWG.Done()
		s.acceptSerialRequests(port, address)
	}()

	return err
}

func (s *Server) acceptSerialRequests(port serial.Port, address uint8) {
	const charTimeMicros = 750
	const charTimeout = (charTimeMicros * 1.5) * time.Microsecond
	const packetTimeout = 3 * charTimeMicros * time.Microsecond
	charTimer := time.NewTimer(charTimeout)
	packetTimer := time.NewTimer(packetTimeout)
	defer charTimer.Stop()
	defer packetTimer.Stop()

SkipFrameError:
	for {
		select {
		case <-s.portsCloseChan:
			return
		default:
		}

		messageBuffer := make([]byte, 512)
		b := make([]byte, 1)

		// first, try to read a byte, waiting for the first byte in the message
		bytesRead, err := port.Read(b)
		if err != nil {
			// We just want to eat the timeout error and keep trying to handle requests.
			if err == serial.ErrTimeout {
				continue
			}
			if err != io.EOF {
				log.Printf("serial read error %v\n", err)
			}
			return
		}

		if bytesRead == 0 {
			continue
		}

		if bytesRead == 1 {
			// If we read a byte, start the timer for the packet.
			charTimer.Reset(charTimeout)
			packetTimer.Reset(packetTimeout)
			messageBuffer[0] = b[0]
			i := 1
			for {
				b, err := readByte(port, *charTimer, charTimeout)
				if err == errRtuCharTimeout {
					select {
					case <-packetTimer.C:
						messageBuffer[i] = b
						handleFrame(messageBuffer[:i], port, address, s.requestChan)
						break
					case <-charTimer.C:
						continue SkipFrameError
					}
				}
				if err != nil {
					log.Printf("error reading byte: %v\n", err)
					continue SkipFrameError
				}
				messageBuffer[i] = b
			}
		}
	}
}

var errRtuCharTimeout = errors.New("timeout waiting for next byte")

func readByte(port serial.Port, charTimer time.Timer, charTimeout time.Duration) (byte, error) {
	b := make([]byte, 1)
	_, err := port.Read(b)
	if err != nil {
		return 0, err
	}
	select {
	case <-charTimer.C:
		log.Printf("error: more than 750 microseconds passed between bytes received")
		return 0, errRtuCharTimeout
	default:
		charTimer.Reset(charTimeout)
		return b[0], nil
	}
}

func handleFrame(packet []byte, port serial.Port, address uint8, requestChan chan *Request) {
	frame, err := NewRTUFrame(packet)
	if err != nil {
		log.Printf("bad serial frame error %v\n", err)
		return
	}

	// If the frame is not a broadcast, check the address.
	if frame.Address != 0 && frame.Address != address {
		log.Printf("slave id mismatch %v, ignoring message\n", frame.Address)
		return
	}

	request := &Request{port, frame}

	requestChan <- request
}
