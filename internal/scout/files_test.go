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
		expectedPath  string
		expectedError bool
	}{
		// Basic patterns
		{"login:pass@192.168.10.18:/tmp/filename", "login", "pass", "192.168.10.18", "/tmp/filename", false},
		{"user@192.168.10.18:/tmp/filename", "user", "", "192.168.10.18", "/tmp/filename", false},
		{"192.168.10.18:/tmp/filename", "", "", "192.168.10.18", "/tmp/filename", false},

		// Path variation
		{"login:pass@192.168.10.18:/", "login", "pass", "192.168.10.18", "/", false},

		// Error cases
		{"", "", "", "", "", true},
		{"login:pass@192.168.10.18", "login", "pass", "192.168.10.18", "", true},
		{"login:@192.168.10.18:/tmp/filename", "", "", "", "", true},

		// IPv6 cases
		{"root:pass@[2001:db8::1]:/tmp", "root", "pass", "[2001:db8::1]", "/tmp", false},
		{"[::1]:/var/log", "", "", "[::1]", "/var/log", false},
		{"[2001:db8:85a3:8d3:1319:8a2e:370:7348]:/home", "", "", "[2001:db8:85a3:8d3:1319:8a2e:370:7348]", "/home", false},
		{"user:pass@[::ffff:192.0.2.1]:/var", "user", "pass", "[::ffff:192.0.2.1]", "/var", false},
		{"[fe80::1]:/usr/local", "", "", "[fe80::1]", "/usr/local", false},

		// IPv6 error cases
		{"[2001:db8::1", "", "", "", "", true},
		{"user:pass@[:::1]:/tmp", "", "", "", "", true},
		{"[::/128]:/tmp", "", "", "", "", true},
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

		if rf.Path != tc.expectedPath {
			t.Errorf("Path mismatch for input '%s'. Expected: %s, Got: %s", tc.input, tc.expectedPath, rf.Path)
		}
	}
}
