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
		{"login:pass@192.168.10.18:/tmp/filename", "login", "pass", "192.168.10.18", "/tmp/filename", false},
		{"user@192.168.10.18:/tmp/filename", "user", "", "192.168.10.18", "/tmp/filename", false},
		{"192.168.10.18:/tmp/filename", "", "", "192.168.10.18", "/tmp/filename", false},
		{"login@192.168.10.18:/tmp/filename", "login", "", "192.168.10.18", "/tmp/filename", false},
		{"login:pass@192.168.10.18:/", "login", "pass", "192.168.10.18", "/", false},
		{"login:pass@192.168.10.18", "login", "pass", "192.168.10.18", "", true},
		{"login:pass@192.168.10.18:/tmp", "login", "pass", "192.168.10.18", "/tmp", false},
		{"", "", "", "", "", true},
		{"login:pass@192.168.10.18:/tmp/filename:extra", "", "", "", "", true},
		{"login:pass:extra@192.168.10.18:/tmp/filename", "", "", "", "", true},
		{"login:@192.168.10.18:/tmp/filename", "", "", "", "", true},
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
