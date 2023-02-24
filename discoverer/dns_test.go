package discoverer

import (
	"fmt"
	"testing"
	"time"
)

type handler struct {
}

func (h *handler) OnEndpointUpdate(all, add, del EndpointList) {
	fmt.Println("all:", all)
	fmt.Println("add:", add)
	fmt.Println("del:", del)
}

func TestNewDNS(t *testing.T) {
	dns, err := NewDNS("dev.tglog.com", 20001, 5*time.Second, nil)
	if err != nil {
		t.Error(err)
	}
	fmt.Println("get", dns.GetEndpoints())
	dns.AddEventHandler(&handler{})
	select {}
}
