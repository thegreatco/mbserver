package mbserver

import (
	"io"
	"log"

	"github.com/goburrow/serial"
)

// ListenRTU starts the Modbus server listening to a serial device.
// For example:  err := s.ListenRTU(&serial.Config{Address: "/dev/ttyUSB0"}, 15)
func (s *Server) ListenRTU(serialConfig *serial.Config, address uint8) (err error) {
	port, err := serial.Open(serialConfig)
	if err != nil {
		log.Fatalf("failed to open %s: %v\n", serialConfig.Address, err)
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
SkipFrameError:
	for {
		select {
		case <-s.portsCloseChan:
			return
		default:
		}

		buffer := make([]byte, 512)

		bytesRead, err := port.Read(buffer)
		if err != nil {
			if err != io.EOF {
				log.Printf("serial read error %v\n", err)
			}
			return
		}

		if bytesRead != 0 {

			// Set the length of the packet to the number of read bytes.
			packet := buffer[:bytesRead]

			frame, err := NewRTUFrame(packet)
			if err != nil {
				log.Printf("bad serial frame error %v\n", err)
				//The next line prevents RTU server from exiting when it receives a bad frame. Simply discard the erroneous
				//frame and wait for next frame by jumping back to the beginning of the 'for' loop.
				log.Printf("Keep the RTU server running!!\n")
				continue SkipFrameError
				//return
			}

			if frame.Address != address {
				log.Printf("slave id mismatch %v, ignoring message\n", frame.Address)
				continue
			}

			request := &Request{port, frame}

			s.requestChan <- request
		}
	}
}
