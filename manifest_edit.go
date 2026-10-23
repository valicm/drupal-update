package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Edits change only the bytes they target, so key order, indentation and
// escaping in composer.json stay as Composer or the maintainer wrote them.

type jsonMember struct {
	key        string
	start, end int // key (or array element) through the end of its value
	valueStart int
}

// jsonMembers lists the members of the object or array at path with their offsets.
func jsonMembers(doc []byte, path ...string) ([]jsonMember, int, int, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	tok, e := dec.Token()
	for depth := 0; e == nil; depth++ {
		delim, ok := tok.(json.Delim)
		if !ok || delim != '{' && delim != '[' {
			return nil, 0, 0, fmt.Errorf("%s: %s is not an object or array", ComposerJsonFile, strings.Join(path[:depth], "."))
		}
		open := int(dec.InputOffset()) - 1
		var members []jsonMember
		descend := false
		for dec.More() {
			m := jsonMember{start: skipJSONSeparators(doc, int(dec.InputOffset()))}
			m.valueStart = m.start
			if delim == '{' {
				key, e := dec.Token()
				if e != nil {
					return nil, 0, 0, e
				}
				m.key, _ = key.(string)
				m.valueStart = skipJSONSeparators(doc, int(dec.InputOffset()))
				if depth < len(path) && m.key == path[depth] {
					descend = true
					break
				}
			}
			if e = skipJSONValue(dec); e != nil {
				return nil, 0, 0, e
			}
			m.end = int(dec.InputOffset())
			members = append(members, m)
		}
		if depth == len(path) {
			if _, e = dec.Token(); e != nil {
				return nil, 0, 0, e
			}
			return members, open, int(dec.InputOffset()) - 1, nil
		}
		if !descend {
			return nil, 0, 0, fmt.Errorf("%s: %s not found", ComposerJsonFile, strings.Join(path[:depth+1], "."))
		}
		tok, e = dec.Token()
	}
	return nil, 0, 0, e
}

func skipJSONSeparators(doc []byte, i int) int {
	for i < len(doc) && strings.IndexByte(" \t\r\n,:", doc[i]) >= 0 {
		i++
	}
	return i
}

func skipJSONValue(dec *json.Decoder) error {
	for depth := 0; ; {
		tok, e := dec.Token()
		if e != nil {
			return e
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}

func splice(doc []byte, from, to int, text []byte) []byte {
	return append(append(append([]byte{}, doc[:from]...), text...), doc[to:]...)
}

func editManifest(edit func([]byte) ([]byte, error)) error {
	b, e := os.ReadFile(ComposerJsonFile)
	if e != nil {
		return e
	}
	if b, e = edit(b); e != nil {
		return e
	}
	return os.WriteFile(ComposerJsonFile, b, 0644)
}

// setManifestString replaces the string at path, encoded without Go's HTML escaping.
func setManifestString(value string, path ...string) error {
	var text bytes.Buffer
	enc := json.NewEncoder(&text)
	enc.SetEscapeHTML(false)
	if e := enc.Encode(value); e != nil {
		return e
	}
	return editManifest(func(doc []byte) ([]byte, error) {
		members, _, _, e := jsonMembers(doc, path[:len(path)-1]...)
		if e != nil {
			return nil, e
		}
		for _, m := range members {
			if m.key == path[len(path)-1] {
				return splice(doc, m.valueStart, m.end, bytes.TrimRight(text.Bytes(), "\n")), nil
			}
		}
		return nil, fmt.Errorf("%s: %s not found", ComposerJsonFile, strings.Join(path, "."))
	})
}

// removeManifestMember deletes the first member of the object or array at path
// that match accepts, along with the separator and line that belonged to it.
func removeManifestMember(match func(i int, key string) bool, path ...string) error {
	return editManifest(func(doc []byte) ([]byte, error) {
		members, open, shut, e := jsonMembers(doc, path...)
		if e != nil {
			return nil, e
		}
		for i, m := range members {
			if !match(i, m.key) {
				continue
			}
			switch {
			case i+1 < len(members):
				return splice(doc, m.start, members[i+1].start, nil), nil
			case i > 0:
				return splice(doc, members[i-1].end, m.end, nil), nil
			}
			return splice(doc, open+1, shut, nil), nil
		}
		return nil, fmt.Errorf("%s: no matching member in %s", ComposerJsonFile, strings.Join(path, "."))
	})
}
