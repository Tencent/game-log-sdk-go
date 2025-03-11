package util

import (
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// IPtoUInt converts an IP to an uint64 integer
func IPtoUInt(ip string) uint32 {
	ips := net.ParseIP(ip)

	if len(ips) == 16 {
		return binary.BigEndian.Uint32(ips[12:16])
	} else if len(ips) == 4 {
		return binary.BigEndian.Uint32(ips)
	}
	return 0
}

// IsPrivateIP checks if an IP is a private one, according to the tencent rules
// 内网保留IP参考KM: /q/view/210875
func IsPrivateIP(ip string) bool {
	ipByte := strings.Split(ip, ".")
	if len(ipByte) != 4 {
		return false
	}

	first, err := strconv.Atoi(ipByte[0])
	if err != nil {
		return false
	}

	second, err := strconv.Atoi(ipByte[1])
	if err != nil {
		return false
	}

	third, err := strconv.Atoi(ipByte[2])
	if err != nil {
		return false
	}

	fourth, err := strconv.Atoi(ipByte[3])
	if err != nil {
		return false
	}

	if first == 11 || first == 10 || first == 9 || first == 30 || first == 21 ||
		(first == 100 && second >= 64 && second <= 127) ||
		(first == 172 && second >= 16 && second <= 31) ||
		(first == 192 && second == 168) ||
		(first == 172 && second == 32 && (third == 0 || third == 1) && fourth >= 1 && fourth <= 128) {
		return true
	}

	return false
}

// GetPrivateIPList gets all the private IPs of the current host
func GetPrivateIPList() ([]string, error) {
	ips, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	var privateIPs []string
	for _, ip := range ips {
		parts := strings.Split(ip.String(), "/")
		if len(parts) != 2 {
			return nil, fmt.Errorf("ip %v address format error", ip.String())
		}
		if IsPrivateIP(parts[0]) {
			privateIPs = append(privateIPs, parts[0])
		}
	}

	return privateIPs, nil
}

// GetFirstPrivateIP gets the first private IP of the current host
func GetFirstPrivateIP() (string, error) {
	ips, err := GetPrivateIPList()
	if err != nil {
		return "", err
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("no private ip")
	}

	return ips[0], nil
}

// GetFirstIP gets the first IP of the current host
func GetFirstIP() (string, error) {
	ips, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, ip := range ips {
		parts := strings.Split(ip.String(), "/")
		if len(parts) != 2 {
			continue
		}
		return parts[0], nil
	}

	return "", fmt.Errorf("no ip")
}
