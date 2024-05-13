package telnet

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"net"
)

// Code from https://github.com/Tanjmaxalb/telnet-client - unsupported

const (
	// IAC interpret as command
	IAC = 255
	// SB is subnegotiation of the indicated option follows
	SB = 250
	// SE is end of subnegotiation parameters
	SE = 240
	// WILL indicates the desire to begin
	WILL = 251
	// WONT indicates the refusal to perform,
	// continue performing, the indicated option
	WONT = 252
	// DO indicates the request that the other
	// party perform, or confirmation that you are
	// expecting the other party to perform, the indicated option
	DO = 253
	// DONT indicates the demand that the other
	// party stop performing, or confirmation that you
	// are no longer expecting the other party to
	// perform, the indicated option
	DONT = 254
)

const (
	loginRe    = "login:"
	passwordRe = "Password:"
)

var bannerRe *regexp.Regexp = regexp.MustCompile("\\$|#")

// TelnetClient is basic descriptor
type TelnetClient struct {
	Login     string
	Password  string
	Address   string
	Port      string
	Timeout   time.Duration
	Verbose   bool
	LogWriter *bufio.Writer
	reader    *bufio.Reader
	writer    *bufio.Writer
	conn      net.Conn
}

func (tc *TelnetClient) setDefaultParams() {
	if tc.Port == "" {
		tc.Port = "23"
	}
	if tc.Timeout == 0 {
		tc.Timeout = 10 * time.Second
	}
	if tc.Verbose && tc.LogWriter == nil {
		tc.LogWriter = bufio.NewWriter(os.Stdout)
	}
}

func (tc *TelnetClient) log(format string, params ...interface{}) {
	if tc.Verbose {
		fmt.Fprintf(tc.LogWriter, "telnet: "+format+"\n", params...)
		tc.LogWriter.Flush()
	}
}

// Dial does open connect to telnet server
func (tc *TelnetClient) Dial() (err error) {
	tc.setDefaultParams()

	tc.log("Trying connect to %s:%s", tc.Address, tc.Port)
	tc.conn, err = net.Dial("tcp", tc.Address+":"+tc.Port)
	if err != nil {
		return
	}

	tc.reader = bufio.NewReader(tc.conn)
	tc.writer = bufio.NewWriter(tc.conn)
	err = tc.conn.SetReadDeadline(time.Now().Add(tc.Timeout))
	if err != nil {
		return
	}

	// Negotiate Telnet options to turn off local echoing
	tc.log("Negotiating Telnet options to disable local echoing")
	tc.writer.Write([]byte{IAC, WILL, 1}) // WILL ECHO
	tc.writer.Write([]byte{IAC, WONT, 3}) // WONT Suppress Go Ahead
	tc.writer.Flush()

	tc.log("Waiting for the first banner")
	err = tc.waitWelcomeSigns()

	return
}

func (tc *TelnetClient) Close() {
	tc.conn.Close()
}

func (tc *TelnetClient) skipSBSequence() (err error) {
	var peeked []byte

	for {
		_, err = tc.reader.Discard(1)
		if err != nil {
			return
		}

		peeked, err = tc.reader.Peek(2)
		if err != nil {
			return
		}

		if peeked[0] == IAC && peeked[1] == SE {
			_, err = tc.reader.Discard(2)
			break
		}
	}

	return
}

func (tc *TelnetClient) skipCommand() (err error) {
	var peeked []byte

	peeked, err = tc.reader.Peek(1)
	if err != nil {
		return
	}

	switch peeked[0] {
	case WILL, WONT, DO, DONT:
		_, err = tc.reader.Discard(2)
	case SB:
		err = tc.skipSBSequence()
	}

	return
}

// ReadByte receives byte from remote server, avoiding commands
func (tc *TelnetClient) ReadByte() (b byte, err error) {
	for {
		b, err = tc.reader.ReadByte()
		if err != nil || b != IAC {
			break
		}

		err = tc.skipCommand()
		if err != nil {
			break
		}
	}

	return
}

// SkipBytes skip a number of bytes (typically, sent command echo)
func (tc *TelnetClient) SkipBytes(n int) error {
	for i := 0; i < n; i++ {
		_, err := tc.reader.ReadByte()
		if err != nil {
			return err
		}
	}

	return nil
}

// ReadUntil reads bytes until a specific symbol.
// Delimiter character will be written to result buffer
func (tc *TelnetClient) ReadUntil(data *[]byte, delim byte) (n int, err error) {
	var b byte

	for b != delim {
		b, err = tc.ReadByte()
		if err != nil {
			break
		}

		*data = append(*data, b)
		n++
	}

	return
}

func findNewLinePos(data []byte) int {
	var pb byte

	for i := len(data) - 1; i >= 0; i-- {
		cb := data[i]
		if pb == '\n' && cb == '\r' {
			return i
		}

		pb = cb
	}

	return -1
}

// ReadUntilPrompt reads data until process function stops.
// If process function returns true, reading will be stopped
// Process function give chunk of line i.e. from start of line
// to last white space or whole line, if next line delimiter is found
func (tc *TelnetClient) ReadUntilPrompt(
	process func(data []byte) bool,
) (output []byte, err error) {
	var n int
	var delimPos int
	var linePos int
	var chunk []byte

	output = make([]byte, 0, 64*1024)

	for {
		// Usually, if system print a prompt,
		// it requires inputing data and
		// prompt has ':' or whitespace in end of line.
		// However, may be cases which have another behaviors.
		// So client may freeze
		n, err = tc.ReadUntil(&output, ' ')
		if err != nil {
			return
		}

		delimPos += n
		n = findNewLinePos(output)
		if n != -1 {
			linePos = n + 2
		}

		chunk = output[linePos:delimPos]

		if process(chunk) {
			break
		}
	}

	return
}

// ReadUntilBanner reads until banner, i.e. whole output from command
func (tc *TelnetClient) ReadUntilBanner() (output []byte, err error) {
	output, err = tc.ReadUntilPrompt(func(data []byte) bool {
		m := bannerRe.Find(data)
		return len(m) > 0
	})

	output = bannerRe.ReplaceAll(output, []byte{})
	output = bytes.Trim(output, " ")

	return
}

func (tc *TelnetClient) findInputPrompt(
	prompt string,
	responce string,
	data string,
) bool {
	if strings.Contains(data, prompt) {
		tc.Write([]byte(responce + "\r\n"))
		return true
	}

	return false
}

// waitWelcomeSigns waits for appearance of the first banner
// If detect login prompt, it will authorize
func (tc *TelnetClient) waitWelcomeSigns() (err error) {
	_, err = tc.ReadUntilPrompt(func(data []byte) bool {
		if tc.findInputPrompt(loginRe, tc.Login, string(data)) {
			tc.log("Found login prompt")
			return false
		}
		if tc.findInputPrompt(passwordRe, tc.Password, string(data)) {
			tc.log("Found password prompt")
			return false
		}

		m := bannerRe.Find(data)
		return len(m) > 0
	})

	return
}

// Write sends raw data to remove telnet server
func (tc *TelnetClient) Write(data []byte) (n int, err error) {
	n, err = tc.writer.Write(data)
	if err == nil {
		err = tc.writer.Flush()
	}

	return
}

// Execute sends command on remote server and returns whole output
func (tc *TelnetClient) Execute(
	name string,
	args ...string,
) (stdout []byte, err error) {
	_, err = tc.reader.Discard(tc.reader.Buffered())
	if err != nil {
		return
	}

	request := []byte(name + " " + strings.Join(args, " ") + "\r\n")
	tc.log("Send command: %s", request[:len(request)-2])
	tc.Write(request)

	err = tc.SkipBytes(len(request))
	if err != nil {
		return
	}

	stdout, err = tc.ReadUntilBanner()
	if err != nil {
		return
	}
	tc.log("Received data with size = %d", len(stdout))

	return
}
