// udp_linux.go
//go:build linux
// +build linux

package udp

import (
	"net"
	"os"
	"syscall"
)

func createUDPSocket(localAddr, remoteAddr *net.UDPAddr) (*net.UDPConn, error) {
	var domain int
	var localSockAddr, remoteSockAddr syscall.Sockaddr

	if localAddr.IP.To4() != nil {
		// 本地地址为IPv4
		domain = syscall.AF_INET
		localSockAddr = &syscall.SockaddrInet4{Port: localAddr.Port}
		copy(localSockAddr.(*syscall.SockaddrInet4).Addr[:], localAddr.IP.To4())
	} else {
		// 本地地址为IPv6
		domain = syscall.AF_INET6
		localSockAddr = &syscall.SockaddrInet6{Port: localAddr.Port}
		copy(localSockAddr.(*syscall.SockaddrInet6).Addr[:], localAddr.IP.To16())
	}

	if remoteAddr.IP.To4() != nil {
		// 远程地址为IPv4
		remoteSockAddr = &syscall.SockaddrInet4{Port: remoteAddr.Port}
		copy(remoteSockAddr.(*syscall.SockaddrInet4).Addr[:], remoteAddr.IP.To4())
	} else {
		// 远程地址为IPv6
		remoteSockAddr = &syscall.SockaddrInet6{Port: remoteAddr.Port}
		copy(remoteSockAddr.(*syscall.SockaddrInet6).Addr[:], remoteAddr.IP.To16())
	}

	// 创建UDP套接字
	fd, err := syscall.Socket(domain, syscall.SOCK_DGRAM, syscall.IPPROTO_UDP)
	if err != nil {
		return nil, err
	}

	// 绑定本地地址
	if err := syscall.Bind(fd, localSockAddr); err != nil {
		_ = syscall.Close(fd)
		return nil, err
	}

	// 连接到远程地址
	if err := syscall.Connect(fd, remoteSockAddr); err != nil {
		_ = syscall.Close(fd)
		return nil, err
	}

	// 将文件描述符转换为net.UDPConn
	file := os.NewFile(uintptr(fd), "")
	conn, err := net.FileConn(file)
	if err != nil {
		_ = syscall.Close(fd)
		return nil, err
	}

	return conn.(*net.UDPConn), nil
}
