package storage

import (
	"strings"
)

// Registry maps backend driver kinds to implementations.
type Registry struct {
	drivers map[string]Driver
}

// NewRegistry creates a registry from the supplied drivers.
func NewRegistry(drivers ...Driver) *Registry {
	registry := &Registry{drivers: make(map[string]Driver, len(drivers))}
	for _, driver := range drivers {
		if driver != nil {
			registry.drivers[driver.Kind()] = driver
		}
	}
	return registry
}

// NewDefaultRegistry creates the drivers implemented by this build.
func NewDefaultRegistry() *Registry {
	return NewRegistry(NewKoujaDriver())
}

// Driver returns the driver for kind.
func (r *Registry) Driver(kind string) (Driver, bool) {
	if r == nil {
		return nil, false
	}
	driver, ok := r.drivers[strings.ToLower(strings.TrimSpace(kind))]
	return driver, ok
}

// Register adds or replaces a driver.
func (r *Registry) Register(driver Driver) {
	if r == nil || driver == nil {
		return
	}
	if r.drivers == nil {
		r.drivers = make(map[string]Driver)
	}
	r.drivers[driver.Kind()] = driver
}
