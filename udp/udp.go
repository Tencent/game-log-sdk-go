package udp

import (
	"errors"
	"net"
	"time"
)

// Reachable test if an address is reachable by dialing with a request
func Reachable(addr string, req []byte, dialTimeout, readTimeout time.Duration) error {
	// 先用TCP测试IP是否可达
	tcpConn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		var netErr net.Error
		ok := errors.As(err, &netErr)
		// 非网络错误认为不可达
		if !ok {
			return err
		}

		// 超时认为不可达
		if netErr.Timeout() {
			return err
		}
		// 其他错误，继续
	} else {
		_ = tcpConn.Close()
	}

	// 目标地址
	remoteAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}

	// 本地地址
	localAddr, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
	if err != nil {
		return err
	}

	// 创建UDP套接字
	conn, err := createUDPSocket(localAddr, remoteAddr)
	if err != nil {
		return err
	}
	defer func(conn *net.UDPConn) {
		_ = conn.Close()
	}(conn)

	// 发送数据
	if _, err := conn.Write(req); err != nil {
		return err
	}

	// 设置超时时间
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))

	// 尝试读取数据或错误消息
	buf := make([]byte, 1024)
	_, _, err = conn.ReadFrom(buf)
	if err != nil {
		var netErr net.Error
		ok := errors.As(err, &netErr)
		// 非网络错误认为不可达
		if !ok {
			return err
		}

		// 只是超时认为可达
		if netErr.Timeout() {
			return nil
		}
		// 其他原因认为不可达
		return err
	}

	// 没错误认为可达
	return nil
}
