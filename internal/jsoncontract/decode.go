// Package jsoncontract gives authenticated and security-sensitive JSON one
// bounded interpretation: no duplicate fields, unknown fields or extra document.
package jsoncontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

func Decode(data []byte, target any) error {
	scanner := json.NewDecoder(bytes.NewReader(data))
	if err := consume(scanner, 0); err != nil {
		return errors.New("JSON document is ambiguous or invalid")
	}
	if _, err := scanner.Token(); err != io.EOF {
		return errors.New("JSON document has trailing input")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("JSON document schema is invalid")
	}
	return nil
}
func consume(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON depth exceeds bounds")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		fields := map[string]bool{}
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || fields[key] {
				return errors.New("duplicate JSON key")
			}
			fields[key] = true
			if err := consume(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := consume(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter == '{' && closing != json.Delim('}') || delimiter == '[' && closing != json.Delim(']') {
		return errors.New("unexpected closing delimiter")
	}
	return nil
}
