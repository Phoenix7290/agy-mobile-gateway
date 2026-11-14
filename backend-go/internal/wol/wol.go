package wol

import (
	"encoding/hex"
	"fmt"
	"net"
	"strings"
)

// BuildMagicPacket constructs the standard 102-byte Wake-on-LAN UDP payload.
func BuildMagicPacket(macAddr string) ([]byte, error) {
	cleaned := strings.NewReplacer(":", "", "-", "", ".", "").Replace(strings.TrimSpace(macAddr))
	macBytes, err := hex.DecodeString(cleaned)
	if err != nil || len(macBytes) != 6 {
		return nil, fmt.Errorf("MAC address inválido: %q", macAddr)
	}

	packet := make([]byte, 6+16*6)
	for i := 0; i < 6; i++ {
		packet[i] = 0xff
	}
	for i := 0; i < 16; i++ {
		copy(packet[6+i*6:], macBytes)
	}
	return packet, nil
}

// SendMagicPacket broadcasts a Wake-on-LAN packet over UDP port 9.
func SendMagicPacket(macAddr string) error {
	packet, err := BuildMagicPacket(macAddr)
	if err != nil {
		return err
	}
	bcastAddr := &net.UDPAddr{IP: net.IPv4bcast, Port: 9}
	conn, err := net.DialUDP("udp4", nil, bcastAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Write(packet)
	return err
}
