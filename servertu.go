package mbserver

import (
	"fmt"
	"io"
	"log"

	"github.com/goburrow/serial"
)

type rtuPacket struct {
	address            byte
	functionCode       byte
	data               []byte
	crc                []byte
	expectedDataLength int
}

func newRtuPacket(b byte) *rtuPacket {
	return &rtuPacket{address: b, data: make([]byte, 0), crc: make([]byte, 0)}
}

func (p *rtuPacket) Data() []byte {
	data := make([]byte, 1+1+len(p.data)+2)
	data[0] = p.address
	data[1] = p.functionCode
	copy(data[2:], p.data)
	copy(data[2+len(p.data):], p.crc)
	return data
}

func (p *rtuPacket) appendReadRegistersData(b byte) bool {
	// We're still collecting address information
	if len(p.data) < 2 {
		p.data = append(p.data, b)
		return false
	} else if len(p.data) >= 2 && len(p.data) < 4 {
		// We're collecting number of registers/coils to read
		p.data = append(p.data, b)
		return false
	} else if len(p.data) >= 4 {
		p.crc = append(p.crc, b)
		if len(p.crc) == 2 {
			return true
		} else {
			return false
		}
	} else {
		panic("should not get here")
	}
}

func (p *rtuPacket) appendWriteSingleRegisterData(b byte) bool {
	// We're still collecting address information
	if len(p.data) < 2 {
		p.data = append(p.data, b)
		return false
	} else if len(p.data) >= 2 && len(p.data) < 4 {
		p.data = append(p.data, b)
		return false
	} else if len(p.data) >= 4 {
		p.crc = append(p.crc, b)
		if len(p.crc) == 2 {
			return true
		} else {
			return false
		}
	}

	panic("should not get here")
}

func (p *rtuPacket) appendWriteMultipleRegistersData(b byte) bool {
	// We're still collecting address information
	if len(p.data) < 2 {
		p.data = append(p.data, b)
		return false
	} else if len(p.data) >= 2 && len(p.data) < 4 {
		p.data = append(p.data, b)
		return false
	} else if len(p.data) == 4 {
		p.data = append(p.data, b)
		p.expectedDataLength = int(b)
		return false
	} else if len(p.data) > 4 && len(p.data) < 5+p.expectedDataLength {
		p.data = append(p.data, b)
		return false
	} else if len(p.data) >= 5+p.expectedDataLength {
		p.crc = append(p.crc, b)
		if len(p.crc) == 2 {
			return true
		} else {
			return false
		}
	}

	panic("should not get here")
}

func (p *rtuPacket) appendData(b byte) (bool, error) {
	if p.functionCode == 0 {
		p.functionCode = b
		return false, nil
	}
	switch p.functionCode {
	case 1:
		fallthrough
	case 2:
		fallthrough
	case 3:
		fallthrough
	case 4:
		return p.appendReadRegistersData(b), nil
	case 5:
		fallthrough
	case 6:
		return p.appendWriteSingleRegisterData(b), nil
	case 15:
		fallthrough
	case 16:
		return p.appendWriteMultipleRegistersData(b), nil
	}
	return false, fmt.Errorf("unknown function code % x", p.functionCode)
}

// ListenRTU starts the Modbus server listening to a serial device.
// For example:  err := s.ListenRTU(&serial.Config{Address: "/dev/ttyUSB0"})
func (s *Server) ListenRTU(serialConfig *serial.Config, slaveId uint8) (err error) {
	port, err := serial.Open(serialConfig)
	if err != nil {
		log.Fatalf("failed to open %s: %v\n", serialConfig.Address, err)
	}
	s.ports = append(s.ports, port)

	s.portsWG.Add(1)
	go func() {
		defer s.portsWG.Done()
		s.acceptSerialRequests(port, slaveId)
	}()

	return err
}

func (s *Server) acceptSerialRequests(port serial.Port, slaveId uint8) {
	b := make([]byte, 1)
	var packet *rtuPacket
	for {
		bytesRead, err := port.Read(b)
		if err != nil {
			if err == serial.ErrTimeout {
				// log.Printf("serial read timeout, tossing packet: %v\n", err)
				// i = 0
				continue
			}
			if err == io.EOF {
				log.Printf("serial read eof, exiting: %v\n", err)
				return
			}
			log.Printf("serial read error %v\n", err)
			return
		}
		if bytesRead == 0 {
			continue
		}
		if bytesRead == 1 {
			if packet == nil {
				packet = newRtuPacket(b[0])
				if packet.address != slaveId {
					packet = nil
					continue
				}
			} else {
				res, err := packet.appendData(b[0])
				if err != nil {
					log.Printf("error appending data: %v\n", err)
					packet = nil
					continue
				}
				if res {
					frame, err := NewRTUFrame(packet.Data())
					if err != nil {
						log.Printf("bad serial frame error %v\n", err)
						packet = nil
						continue
					}
					request := &Request{port, frame}
					s.requestChan <- request
					packet = nil
					continue
				}
			}
		}
	}
}
