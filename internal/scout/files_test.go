package scout

import (
	"testing"
)

func TestParseRemoteFileName(t *testing.T) {
	testCases := []struct {
		input         string
		expectedUser  string
		expectedPass  string
		expectedHost  string
		expectedPort  string
		expectedPath  string
		expectedError bool
	}{
		// Basic patterns
		{"login:pass@192.168.10.18:/tmp/filename", "login", "pass", "192.168.10.18", "23", "/tmp/filename", false},
		{"user@192.168.10.18:/tmp/filename", "user", "", "192.168.10.18", "23", "/tmp/filename", false},
		{"192.168.10.18:/tmp/filename", "", "", "192.168.10.18", "23", "/tmp/filename", false},

		// Path variation
		{"login:pass@192.168.10.18:/", "login", "pass", "192.168.10.18", "23", "/", false},

		// With telnet port
		{"login:pass@192.168.10.18:2323:/tmp/filename", "login", "pass", "192.168.10.18", "2323", "/tmp/filename", false},

		// Error cases
		{"", "", "", "", "", "", true},
		{"login:pass@192.168.10.18", "login", "pass", "192.168.10.18", "23", "", true},
		{"login:@192.168.10.18:/tmp/filename", "", "", "", "23", "", true},

		// IPv6 cases
		{"root:pass@[2001:db8::1]:/tmp", "root", "pass", "[2001:db8::1]", "23", "/tmp", false},
		{"[::1]:/var/log", "", "", "[::1]", "23", "/var/log", false},
		{"[2001:db8:85a3:8d3:1319:8a2e:370:7348]:/home", "", "", "[2001:db8:85a3:8d3:1319:8a2e:370:7348]", "23", "/home", false},
		{"user:pass@[::ffff:192.0.2.1]:/var", "user", "pass", "[::ffff:192.0.2.1]", "23", "/var", false},
		{"[fe80::1]:/usr/local", "", "", "[fe80::1]", "23", "/usr/local", false},

		// IPv6 error cases
		{"[2001:db8::1", "", "", "", "23", "", true},
		{"user:pass@[:::1]:/tmp", "", "", "", "23", "", true},
		{"[::/128]:/tmp", "", "", "", "23", "", true},
	}

	for _, tc := range testCases {
		rf, err := ParseRemoteFileName(tc.input)

		if (err != nil) != tc.expectedError {
			t.Errorf("Unexpected error for input '%s'. Got error: %v", tc.input, err)
			continue
		}
		if err != nil {
			continue
		}

		if rf.Username != tc.expectedUser {
			t.Errorf("Username mismatch for input '%s'. Expected: %s, Got: %s", tc.input, tc.expectedUser, rf.Username)
		}

		if rf.Password != tc.expectedPass {
			t.Errorf("Password mismatch for input '%s'. Expected: %s, Got: %s", tc.input, tc.expectedPass, rf.Password)
		}

		if rf.Host != tc.expectedHost {
			t.Errorf("Host mismatch for input '%s'. Expected: %s, Got: %s", tc.input, tc.expectedHost, rf.Host)
		}

		if rf.Port != tc.expectedPort {
			t.Errorf("Port mismatch for input '%s'. Expected: %s, Got: %s", tc.input, tc.expectedPort, rf.Port)
		}

		if rf.Path != tc.expectedPath {
			t.Errorf("Path mismatch for input '%s'. Expected: %s, Got: %s", tc.input, tc.expectedPath, rf.Path)
		}
	}
}
