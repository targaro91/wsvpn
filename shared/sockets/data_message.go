package sockets

import (
	"bytes"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// Packet wire format:
// First byte: 1 bit "last fragment", 7 bits uint for index within current packet (first fragment is 0, etc)
// a) If "last fragment" is set and index is 0, data that follows is payload
// b) In any other case, this is followed by a 32 bit identifier
// Example for case a): [10000000] PAYLOAD
// Example for case b): [00000000] [00000000 00000000 00000000 00000001] PAYLOAD_PART_0
//                      [10000001] [00000000 00000000 00000000 00000001] PAYLOAD_PART_1 (last fragment)

type fragmentsInfo struct {
	lastIndex int
	data      map[uint8][]byte
	time      time.Time
}

const fragmentExpiryTime = time.Second * time.Duration(30)
const fragmentationMinProtocol = 10

func (s *Socket) processPacket(packet []byte) bool {
	if s.packetHandler != nil {
		res, err := s.packetHandler.HandlePacket(s, packet)
		if err != nil {
			s.log.Printf("Error in packet handler: %v", err)
			return false
		}
		if res {
			return true
		}
	}

	if s.iface == nil {
		return true
	}
	s.iface.Write(packet)
	return true
}

func (s *Socket) dataMessageHandler(message []byte) bool {
	if s.remoteProtocolVersion < fragmentationMinProtocol {
		return s.processPacket(message)
	}

	fragHeader := message[0]
	if fragHeader == 0b10000000 { // Last fragment at index 0 => unfragmented packet
		return s.processPacket(message[1:])
	}

	fragIndex := fragHeader & 0b01111111
	isLastFragment := fragHeader&0b10000000 == 0b10000000
	packetId := (uint32(message[1]) << 24) | (uint32(message[2]) << 16) | (uint32(message[3]) << 8) | uint32(message[4])

	s.defragLock.Lock()
	fragInfo := s.defragBuffer[packetId]
	if fragInfo == nil {
		fragInfo = &fragmentsInfo{
			lastIndex: -1000, // Very small value as an indicator for "not set, yet"
			data:      make(map[uint8][]byte),
		}
		s.defragBuffer[packetId] = fragInfo
	}

	fragInfo.time = time.Now()
	fragInfo.data[fragIndex] = message[5:]
	if isLastFragment {
		fragInfo.lastIndex = int(fragIndex)
	}

	if len(fragInfo.data) == fragInfo.lastIndex+1 {
		delete(s.defragBuffer, packetId)
		s.defragLock.Unlock()

		buf := &bytes.Buffer{}
		for i := uint8(0); i <= uint8(fragInfo.lastIndex); i++ {
			buf.Write(fragInfo.data[i])
		}
		return s.processPacket(buf.Bytes())
	}

	s.defragLock.Unlock()
	return true
}

func (s *Socket) cleanupFragmentsLoop() {
	for {
		t := <-s.fragmentCleanupTicker.C
		s.cleanupFragments()
		if t.IsZero() {
			break
		}
	}
}

func (s *Socket) cleanupFragments() {
	s.defragLock.Lock()
	defer s.defragLock.Unlock()

	deleteIndices := make([]uint32, 0)

	for idx, fragInfo := range s.defragBuffer {
		if time.Since(fragInfo.time) > fragmentExpiryTime {
			deleteIndices = append(deleteIndices, idx)
		}
	}

	for _, idx := range deleteIndices {
		delete(s.defragBuffer, idx)
	}
}

func (s *Socket) sendDataWithError(data []byte) error {
	err := s.adapter.WriteDataMessage(data)
	if err != nil {
		s.CloseError(fmt.Errorf("error sending data message: %v", err))
	}
	return err
}

func (s *Socket) WritePacket(data []byte) error {
	if s.remoteProtocolVersion < fragmentationMinProtocol {
		return s.sendDataWithError(data)
	}

	realDataLen := len(data)
	if realDataLen <= 0 || realDataLen > 0xFFFF {
		err := errors.New("packet size out of bounds")
		s.CloseError(err)
		return err
	}

	maxLen := s.adapter.MaxDataPayloadLen()
	dataLen := uint16(realDataLen)

	buf := &bytes.Buffer{}
	if dataLen+1 <= maxLen {
		buf.WriteByte(0b10000000)
		buf.Write(data)
		return s.sendDataWithError(buf.Bytes())
	}

	packetId := atomic.AddUint32(&s.lastFragmentId, 1)

	maxLen -= 5 // 5 byte header (frag|LF ID ID ID ID)!
	fragmentCount := uint16(dataLen / maxLen)
	if dataLen%maxLen != 0 {
		fragmentCount++
	}

	packetId1 := uint8(packetId % 0xFF)
	packetId2 := uint8((packetId >> 8) % 0xFF)
	packetId3 := uint8((packetId >> 16) % 0xFF)
	packetId4 := uint8((packetId >> 24) % 0xFF)
	for frag := uint16(0); frag < fragmentCount; frag++ {
		buf.Reset()

		fragFlag := uint8(frag)
		if frag == fragmentCount-1 {
			fragFlag |= 0b10000000
		}
		buf.WriteByte(fragFlag)
		buf.WriteByte(packetId4)
		buf.WriteByte(packetId3)
		buf.WriteByte(packetId2)
		buf.WriteByte(packetId1)

		fragStart := frag * maxLen
		fragEnd := fragStart + maxLen
		if fragEnd > dataLen {
			fragEnd = dataLen
		}
		buf.Write(data[fragStart:fragEnd])
		err := s.sendDataWithError(buf.Bytes())
		if err != nil {
			return err
		}
	}

	return nil
}
