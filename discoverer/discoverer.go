package discoverer

// Endpoint is the config of an endpoint
type Endpoint struct {
	Addr string
	Host string
	Port int
}

// EndpointList is the list of endpoints
type EndpointList []Endpoint

// Addresses returns the addresses of an endpoint list
func (el EndpointList) Addresses() []string {
	addrs := make([]string, 0, len(el))
	for _, endpoint := range el {
		addrs = append(addrs, endpoint.Addr)
	}

	return addrs
}

// EventHandler is the interface of the discoverer
type EventHandler interface {
	OnEndpointDel(EndpointList)
	OnEndpointAdd(EndpointList)
}

// EndpointProvider is the interface of an endpoint provider
type EndpointProvider interface {
	// GetEndpoint gets one Endpoint from the endpoint list with round-robin load balancing policy
	GetEndpoint() (Endpoint, error)

	// GetEndpoints gets all endpoints from the load balancer, since the discoverer will update internally,
	// the caller should copy the endpoint list to its own routine space
	GetEndpoints() EndpointList
}

// Discoverer is the interface of a service discoverer
type Discoverer interface {
	EndpointProvider

	// AddEventHandler adds an EventHandler to the discoverer
	AddEventHandler(h EventHandler)

	// DelEventHandler remove an EventHandler from the discoverer
	DelEventHandler(h EventHandler)

	// Close closes the discoverer
	Close()
}
