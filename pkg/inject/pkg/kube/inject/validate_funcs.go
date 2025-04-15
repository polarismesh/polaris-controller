package inject

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

func validateCIDRList(cidrs string) error {
	if len(cidrs) > 0 {
		for _, cidr := range strings.Split(cidrs, ",") {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return fmt.Errorf("failed parsing cidr '%s': %v", cidr, err)
			}
		}
	}
	return nil
}

func splitPorts(portsString string) []string {
	return strings.Split(portsString, ",")
}

func parsePort(portStr string) (int, error) {
	port, err := strconv.ParseUint(strings.TrimSpace(portStr), 10, 16)
	if err != nil {
		return 0, fmt.Errorf("failed parsing port '%d': %v", port, err)
	}
	return int(port), nil
}

func parsePorts(portsString string) ([]int, error) {
	portsString = strings.TrimSpace(portsString)
	ports := make([]int, 0)
	if len(portsString) > 0 {
		for _, portStr := range splitPorts(portsString) {
			port, err := parsePort(portStr)
			if err != nil {
				return nil, fmt.Errorf("failed parsing port '%d': %v", port, err)
			}
			ports = append(ports, port)
		}
	}
	return ports, nil
}

func validatePortList(parameterName, ports string) error {
	if _, err := parsePorts(ports); err != nil {
		return fmt.Errorf("%s invalid: %v", parameterName, err)
	}
	return nil
}

// ValidateIncludeIPRanges validates the includeIPRanges parameter
func ValidateIncludeIPRanges(ipRanges string) error {
	if ipRanges != "*" {
		if e := validateCIDRList(ipRanges); e != nil {
			return fmt.Errorf("includeIPRanges invalid: %v", e)
		}
	}
	return nil
}

// ValidateExcludeIPRanges validates the excludeIPRanges parameter
func ValidateExcludeIPRanges(ipRanges string) error {
	if e := validateCIDRList(ipRanges); e != nil {
		return fmt.Errorf("excludeIPRanges invalid: %v", e)
	}
	return nil
}

// ValidateIncludeInboundPorts validates the includeInboundPorts parameter
func ValidateIncludeInboundPorts(ports string) error {
	if ports != "*" {
		return validatePortList("includeInboundPorts", ports)
	}
	return nil
}

// ValidateExcludeInboundPorts validates the excludeInboundPorts parameter
func ValidateExcludeInboundPorts(ports string) error {
	return validatePortList("excludeInboundPorts", ports)
}

// ValidateExcludeOutboundPorts validates the excludeOutboundPorts parameter
func ValidateExcludeOutboundPorts(ports string) error {
	return validatePortList("excludeOutboundPorts", ports)
}

// validateStatusPort validates the statusPort parameter
func validateStatusPort(port string) error {
	if _, e := parsePort(port); e != nil {
		return fmt.Errorf("excludeInboundPorts invalid: %v", e)
	}
	return nil
}

// nolint
// validateUInt32 validates that the given annotation value is a positive integer.
func validateUInt32(value string) error {
	_, err := strconv.ParseUint(value, 10, 32)
	return err
}

// validateBool validates that the given annotation value is a boolean.
func validateBool(value string) error {
	_, err := strconv.ParseBool(value)
	return err
}
