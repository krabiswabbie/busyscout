package scout

import (
	"errors"
	"net"
	"strings"
)

type RemoteFile struct {
	Username string
	Password string
	Host     string
	Port     string
	Path     string
}

// ParseRemoteFileName is IPv6-compatible now
func ParseRemoteFileName(input string) (*RemoteFile, error) {
	if input == "" {
		return nil, errors.New("empty input")
	}

	var username, password, host, path string
	hostEnd := strings.Index(input, ":/")
	if hostEnd == -1 {
		return nil, errors.New("invalid format: missing path separator")
	}

	hostPart := input[:hostEnd]
	path = input[hostEnd+1:]

	// Handle IPv6 addresses
	if openBracket := strings.Index(hostPart, "["); openBracket > 0 {
		closeBracket := strings.LastIndex(hostPart, "]")
		if closeBracket == -1 {
			return nil, errors.New("invalid IPv6 address: missing closing bracket")
		}
		if closeBracket != len(hostPart)-1 && hostPart[closeBracket+1] != '@' {
			return nil, errors.New("invalid IPv6 format")
		}

		ipv6Addr := hostPart[openBracket+1 : closeBracket]
		if ip := net.ParseIP(ipv6Addr); ip == nil {
			return nil, errors.New("invalid IPv6 address format")
		}
	}

	// Extract credentials
	if atIdx := strings.LastIndex(hostPart, "@"); atIdx != -1 {
		credentials := hostPart[:atIdx]
		host = hostPart[atIdx+1:]

		if strings.Count(credentials, ":") > 1 {
			return nil, errors.New("invalid credentials format")
		}

		credParts := strings.Split(credentials, ":")
		if len(credParts) == 2 {
			if credParts[1] == "" {
				return nil, errors.New("invalid password format")
			}
			username = credParts[0]
			password = credParts[1]
		} else {
			username = credentials
		}
	} else {
		host = hostPart
	}

	// Extract port from host if present
	port := "23"
	if colonIdx := strings.LastIndex(host, ":"); colonIdx != -1 {
		// In case of IPv6 address, ensure the colon is not part of the address
		if closeBracket := strings.LastIndex(host, "]"); (closeBracket != -1 && closeBracket < colonIdx) || closeBracket == -1 {
			hostPart = host[:colonIdx]
			port = host[colonIdx+1:]
			if port == "" {
				return nil, errors.New("invalid port format")
			}
			host = hostPart
		}
	}

	if strings.Count(path, ":") > 0 {
		return nil, errors.New("invalid path format")
	}

	return &RemoteFile{
		Username: username,
		Password: password,
		Host:     host,
		Port:     port,
		Path:     path,
	}, nil
}
