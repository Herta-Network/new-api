package mistral

import (
	"bufio"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/andybalholm/brotli"
	"github.com/tidwall/gjson"
)

// sdkFloat is a number the Mistral SDK types as a float, which Python writes
// with its fraction: 1.0 where Go writes 1.
type sdkFloat float64

func (f sdkFloat) MarshalJSON() ([]byte, error) {
	return []byte(pythonFloat(float64(f))), nil
}

// sdkFloatOf is the float a client sent, as the SDK writes it.
func sdkFloatOf(value *float64) *sdkFloat {
	if value == nil {
		return nil
	}
	f := sdkFloat(*value)
	return &f
}

// pythonFloat writes a float as Python's repr does: the shortest digits that
// read back as the same number, in positional form with at least one decimal
// from 1e-4 up to 1e16, in exponent form outside it.
func pythonFloat(f float64) string {
	scientific := strconv.FormatFloat(f, 'e', -1, 64)
	exponent, _ := strconv.Atoi(scientific[strings.IndexByte(scientific, 'e')+1:])
	if exponent < -4 || exponent >= 16 {
		return scientific
	}
	positional := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(positional, ".") {
		positional += ".0"
	}
	return positional
}

// pythonJSON rewrites a JSON document as the Mistral SDK writes its bodies,
// json.dumps with separators (",", ":"): no whitespace, every character
// outside printable ASCII escaped as \u with lowercase hex (astral ones as a
// surrogate pair), <, > and & as they are, and numbers as Python reads and
// writes them back, so a float keeps its fraction. Anything that is not JSON
// is returned as it came.
func pythonJSON(document []byte) []byte {
	if !gjson.ValidBytes(document) {
		return document
	}
	out := make([]byte, 0, len(document)+len(document)/8)
	for i := 0; i < len(document); {
		switch c := document[i]; {
		case c == '"':
			end := i + 1
			for document[end] != '"' {
				if document[end] == '\\' {
					end++
				}
				end++
			}
			out = appendPythonString(out, document[i+1:end])
			i = end + 1
		case c == '-' || c >= '0' && c <= '9':
			end := i + 1
			for end < len(document) && strings.IndexByte("0123456789+-.eE", document[end]) >= 0 {
				end++
			}
			out = appendPythonNumber(out, string(document[i:end]))
			i = end
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		default:
			out = append(out, c)
			i++
		}
	}
	return out
}

// appendPythonString writes the contents of a JSON string, escapes included,
// as Python's ASCII-only encoder writes them.
func appendPythonString(out []byte, contents []byte) []byte {
	out = append(out, '"')
	for i := 0; i < len(contents); {
		if contents[i] != '\\' {
			r, size := utf8.DecodeRune(contents[i:])
			i += size
			if r > 0xFFFF {
				high, low := utf16.EncodeRune(r)
				out = appendPythonUnit(appendPythonUnit(out, high), low)
				continue
			}
			out = appendPythonUnit(out, r)
			continue
		}
		escape := contents[i+1]
		i += 2
		switch escape {
		case 'u':
			unit, _ := strconv.ParseUint(string(contents[i:i+4]), 16, 16)
			out = appendPythonUnit(out, rune(unit))
			i += 4
		case 'b':
			out = appendPythonUnit(out, '\b')
		case 'f':
			out = appendPythonUnit(out, '\f')
		case 'n':
			out = appendPythonUnit(out, '\n')
		case 'r':
			out = appendPythonUnit(out, '\r')
		case 't':
			out = appendPythonUnit(out, '\t')
		default: // ", \ and /
			out = appendPythonUnit(out, rune(escape))
		}
	}
	return append(out, '"')
}

// appendPythonUnit writes one UTF-16 code unit of a string.
func appendPythonUnit(out []byte, unit rune) []byte {
	switch unit {
	case '"':
		return append(out, '\\', '"')
	case '\\':
		return append(out, '\\', '\\')
	case '\b':
		return append(out, '\\', 'b')
	case '\f':
		return append(out, '\\', 'f')
	case '\n':
		return append(out, '\\', 'n')
	case '\r':
		return append(out, '\\', 'r')
	case '\t':
		return append(out, '\\', 't')
	}
	if unit >= 0x20 && unit < 0x7f {
		return append(out, byte(unit))
	}
	const hex = "0123456789abcdef"
	return append(out, '\\', 'u', hex[unit>>12&0xf], hex[unit>>8&0xf], hex[unit>>4&0xf], hex[unit&0xf])
}

// appendPythonNumber writes a JSON number as Python writes it back after
// reading it: an integer as it is (but -0 as 0), a float in its repr.
func appendPythonNumber(out []byte, number string) []byte {
	if !strings.ContainsAny(number, ".eE") {
		if number == "-0" {
			number = "0"
		}
		return append(out, number...)
	}
	f, err := strconv.ParseFloat(number, 64)
	if err != nil || math.IsInf(f, 0) {
		return append(out, number...)
	}
	return append(out, pythonFloat(f)...)
}

// decodeContent undoes the content coding of a Mistral answer. The channel
// asks for httpx2's codings itself, so Go's transport leaves the body coded.
func decodeContent(resp *http.Response) {
	coding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	switch coding {
	case "gzip", "x-gzip", "deflate", "br":
	default:
		return
	}
	resp.Body = &decodedBody{coded: resp.Body, coding: coding}
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")
	resp.ContentLength = -1
	resp.Uncompressed = true
}

// decodedBody decodes a coded body from its first read, so that waiting for
// a stream's first bytes never holds up the response.
type decodedBody struct {
	coded   io.ReadCloser
	coding  string
	decoded io.Reader
}

func (b *decodedBody) Read(p []byte) (int, error) {
	if b.decoded == nil {
		switch b.coding {
		case "br":
			b.decoded = brotli.NewReader(b.coded)
		case "deflate":
			// deflate is zlib-wrapped, though some servers send it raw; httpx2
			// reads both.
			coded := bufio.NewReader(b.coded)
			header, _ := coded.Peek(2)
			if len(header) == 2 && header[0]&0x0f == 8 && (uint16(header[0])<<8|uint16(header[1]))%31 == 0 {
				reader, err := zlib.NewReader(coded)
				if err != nil {
					return 0, err
				}
				b.decoded = reader
			} else {
				b.decoded = flate.NewReader(coded)
			}
		default:
			reader, err := gzip.NewReader(b.coded)
			if err != nil {
				return 0, err
			}
			b.decoded = reader
		}
	}
	return b.decoded.Read(p)
}

func (b *decodedBody) Close() error {
	return b.coded.Close()
}
