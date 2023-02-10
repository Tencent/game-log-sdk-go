package discoverer

import (
	"fmt"
	"testing"
	"time"
)

type handler struct {
}

func (h *handler) OnEndpointDel(eps []Endpoint) {
	fmt.Println("del:", eps)
}

func (h *handler) OnEndpointAdd(eps []Endpoint) {
	fmt.Println("add:", eps)
}

func TestNewDNS(t *testing.T) {
	dns, err := NewDNS("dev.tglog.com", 20001, 5*time.Second, nil)
	if err != nil {
		t.Error(err)
	}
	fmt.Println(dns.GetEndpoints())
	select {}
}
