package scout

import (
	"fmt"
	"strings"
)

type RemoteFile struct {
	Username string
	Password string
	Host     string
	Path     string
}

func ParseRemoteFileName(remoteFileName string) (*RemoteFile, error) {
	parts := strings.Split(remoteFileName, "@")

	var credsPart, hostPart string
	if len(parts) == 2 {
		credsPart = parts[0]
		hostPart = parts[1]
	} else if len(parts) == 1 {
		hostPart = parts[0]
	} else {
		return nil, fmt.Errorf("invalid remote file name format")
	}

	creds := strings.Split(credsPart, ":")
	var username, password string
	if len(creds) == 2 {
		if creds[1] == "" {
			return nil, fmt.Errorf("invalid credentials format")
		}
		username = creds[0]
		password = creds[1]
	} else if len(creds) == 1 {
		username = creds[0]
	} else if len(creds) > 2 {
		return nil, fmt.Errorf("invalid credentials format")
	}

	hostAndPath := strings.Split(hostPart, ":")
	if len(hostAndPath) != 2 {
		return nil, fmt.Errorf("invalid host and path format")
	}

	host := hostAndPath[0]
	path := hostAndPath[1]

	return &RemoteFile{
		Username: username,
		Password: password,
		Host:     host,
		Path:     path,
	}, nil
}
