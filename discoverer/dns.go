package discoverer

import (
	"errors"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.woa.com/tglog/v3/sdk-go/logger"
)

const (
	ipRegexp = `^((([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])\.){3}([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])$|^(([a-fA-F]|[a-fA-F][a-fA-F0-9\-]*[a-fA-F0-9])\.)*([A-Fa-f]|[A-Fa-f][A-Fa-f0-9\-]*[A-Fa-f0-9])$|^(?:(?:(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})):){6})(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})):(?:(?:[0-9a-fA-F]{1,4})))|(?:(?:(?:(?:(?:25[0-5]|(?:[1-9]|1[0-9]|2[0-4])?[0-9]))\.){3}(?:(?:25[0-5]|(?:[1-9]|1[0-9]|2[0-4])?[0-9])))))))|(?:(?:::(?:(?:(?:[0-9a-fA-F]{1,4})):){5})(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})):(?:(?:[0-9a-fA-F]{1,4})))|(?:(?:(?:(?:(?:25[0-5]|(?:[1-9]|1[0-9]|2[0-4])?[0-9]))\.){3}(?:(?:25[0-5]|(?:[1-9]|1[0-9]|2[0-4])?[0-9])))))))|(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})))?::(?:(?:(?:[0-9a-fA-F]{1,4})):){4})(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})):(?:(?:[0-9a-fA-F]{1,4})))|(?:(?:(?:(?:(?:25[0-5]|(?:[1-9]|1[0-9]|2[0-4])?[0-9]))\.){3}(?:(?:25[0-5]|(?:[1-9]|1[0-9]|2[0-4])?[0-9])))))))|(?:(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})):){0,1}(?:(?:[0-9a-fA-F]{1,4})))?::(?:(?:(?:[0-9a-fA-F]{1,4})):){3})(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})):(?:(?:[0-9a-fA-F]{1,4})))|(?:(?:(?:(?:(?:25[0-5]|(?:[1-9]|1[0-9]|2[0-4])?[0-9]))\.){3}(?:(?:25[0-5]|(?:[1-9]|1[0-9]|2[0-4])?[0-9])))))))|(?:(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})):){0,2}(?:(?:[0-9a-fA-F]{1,4})))?::(?:(?:(?:[0-9a-fA-F]{1,4})):){2})(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})):(?:(?:[0-9a-fA-F]{1,4})))|(?:(?:(?:(?:(?:25[0-5]|(?:[1-9]|1[0-9]|2[0-4])?[0-9]))\.){3}(?:(?:25[0-5]|(?:[1-9]|1[0-9]|2[0-4])?[0-9])))))))|(?:(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})):){0,3}(?:(?:[0-9a-fA-F]{1,4})))?::(?:(?:[0-9a-fA-F]{1,4})):)(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})):(?:(?:[0-9a-fA-F]{1,4})))|(?:(?:(?:(?:(?:25[0-5]|(?:[1-9]|1[0-9]|2[0-4])?[0-9]))\.){3}(?:(?:25[0-5]|(?:[1-9]|1[0-9]|2[0-4])?[0-9])))))))|(?:(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})):){0,4}(?:(?:[0-9a-fA-F]{1,4})))?::)(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})):(?:(?:[0-9a-fA-F]{1,4})))|(?:(?:(?:(?:(?:25[0-5]|(?:[1-9]|1[0-9]|2[0-4])?[0-9]))\.){3}(?:(?:25[0-5]|(?:[1-9]|1[0-9]|2[0-4])?[0-9])))))))|(?:(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})):){0,5}(?:(?:[0-9a-fA-F]{1,4})))?::)(?:(?:[0-9a-fA-F]{1,4})))|(?:(?:(?:(?:(?:(?:[0-9a-fA-F]{1,4})):){0,6}(?:(?:[0-9a-fA-F]{1,4})))?::)))))$`
)

// variables
var (
	errInvalidPort = errors.New("invalid port")
)

// NewDNS create a DNS discoverer
func NewDNS(domain string, port int, lookupInterval time.Duration, log logger.Logger) (Discoverer, error) {
	logger := logger.Std()
	if log != nil {
		logger = log
	}

	if port <= 0 || port > 65535 {
		return nil, errInvalidPort
	}

	if lookupInterval == 0 {
		lookupInterval = 30 * time.Second
	}

	lb := &dnsDiscoverer{
		domain:         domain,
		port:           port,
		lookupInterval: lookupInterval,
		endpointList:   make([]Endpoint, 0),
		hostListMap:    make(map[string]struct{}),
		eventHandlers:  make(map[EventHandler]struct{}),
		log:            logger,
	}

	regex := regexp.MustCompile(ipRegexp)
	// input domain is an IP
	if regex.MatchString(domain) {
		ip := net.ParseIP(domain)
		lb.endpointList = []Endpoint{{Host: ip.String(), Port: port, Addr: buildAddr(ip.String(), port)}}
		lb.hostListStr = ip.String()

		return lb, nil
	}

	// domain
	lb.lookup()
	lb.update()
	return lb, nil
}

type dnsDiscoverer struct {
	sync.RWMutex
	domain         string
	port           int
	lookupInterval time.Duration
	endpointList   EndpointList
	hostListStr    string
	hostListMap    map[string]struct{}
	eventHandlers  map[EventHandler]struct{}
	closeFunc      func()
	log            logger.Logger
}

func (d *dnsDiscoverer) GetEndpoints() EndpointList {
	d.RLock()
	defer d.RUnlock()

	return d.endpointList
}

func (d *dnsDiscoverer) AddEventHandler(h EventHandler) {
	d.Lock()
	defer d.Unlock()

	d.eventHandlers[h] = struct{}{}
}

func (d *dnsDiscoverer) DelEventHandler(h EventHandler) {
	d.Lock()
	defer d.Unlock()

	delete(d.eventHandlers, h)
}

func (d *dnsDiscoverer) Close() {
	if d.closeFunc != nil {
		d.closeFunc()
	}
}

func (d *dnsDiscoverer) lookup() {
	hosts := make(map[string]struct{}, 16)
	for i := 1; i <= 32; i++ {
		lookupHosts, err := net.LookupHost(d.domain)
		if err != nil {
			d.log.Errorf("domain lookup failed: %v", err)
			break
		}

		for _, host := range lookupHosts {
			hosts[host] = struct{}{}
		}

		if len(lookupHosts) > 1 {
			// discoverer server return a list of hosts
			break
		} else {
			// discoverer server return only one ip per request
			if i > len(hosts)*2 && i > 12 {
				break
			}
		}
	}
	// will not update if ip list is empty
	if len(hosts) == 0 {
		d.log.Warnf("no hosts were found from domain: %s, we will keep the local cache", d.domain)
		return
	}

	hostList := make([]string, 0, len(hosts))
	endpointList := make([]Endpoint, 0, len(hosts))
	hostListMap := make(map[string]struct{})
	addEndpoints := make([]Endpoint, 0)
	for host := range hosts {
		hostList = append(hostList, host)
		endpointList = append(endpointList, Endpoint{Host: host, Port: d.port, Addr: buildAddr(host, d.port)})
		hostListMap[host] = struct{}{}

		// new host is not found in the old host list map, it is a new added one
		if _, ok := d.hostListMap[host]; !ok {
			addEndpoints = append(addEndpoints, Endpoint{Host: host, Port: d.port, Addr: buildAddr(host, d.port)})
		}
	}

	delEndpoints := make([]Endpoint, 0)
	for host := range d.hostListMap {
		// old host is not found in the new host list map, it is deleted
		if _, ok := hostListMap[host]; !ok {
			delEndpoints = append(delEndpoints, Endpoint{Host: host, Port: d.port, Addr: buildAddr(host, d.port)})
		}
	}

	d.Lock()
	d.endpointList = endpointList
	d.hostListMap = hostListMap
	d.Unlock()

	// show this logger only in case of ip list change
	sort.Strings(hostList)
	hostListStr := strings.Join(hostList, ";")
	if hostListStr != d.hostListStr {
		d.hostListStr = hostListStr
		d.log.Infof("update domain host list %s: %v", d.domain, hostListStr)
	}

	d.RLock()
	defer d.RUnlock()
	if len(addEndpoints) > 0 {
		for h := range d.eventHandlers {
			h.OnEndpointAdd(addEndpoints)
		}
	}

	if len(delEndpoints) > 0 {
		for h := range d.eventHandlers {
			h.OnEndpointDel(delEndpoints)
		}
	}
}

func (d *dnsDiscoverer) update() func() {
	wg := sync.WaitGroup{}
	ticker := time.NewTicker(d.lookupInterval)
	stopCh := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ticker.C:
				d.lookup()
			case <-stopCh:
				return
			default:
				time.Sleep(500 * time.Millisecond)
			}
		}
	}()

	return func() {
		ticker.Stop()
		close(stopCh)
		wg.Wait()
	}
}

func buildAddr(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
