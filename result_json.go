package gamedig

import (
	"bytes"
	"encoding/json"
)

// MarshalJSON encodes r with node-gamedig's JSON values. Some node-gamedig
// protocols copy a server's string into a top-level field: Quake reports
// password, maxplayers and sometimes numplayers as the server's cvar text,
// such as "password":"0". The Go fields keep their types (Password is
// true for a non-zero value, MaxPlayers is the parsed number) and the
// original value is written to JSON instead, so output stays identical to
// node-gamedig.
func (r Result) MarshalJSON() ([]byte, error) {
	type plain Result // drops this method
	b, err := marshalNoEscape(plain(r))
	if err != nil || len(r.nodeJSON) == 0 {
		return b, err
	}

	// Replace the overridden members, keeping the field order.
	dec := json.NewDecoder(bytes.NewReader(b))
	if _, err := dec.Token(); err != nil { // {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for first := true; dec.More(); first = false {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
		if v, ok := r.nodeJSON[key]; ok {
			if val, err = marshalNoEscape(v); err != nil {
				return nil, err
			}
		}
		if !first {
			out.WriteByte(',')
		}
		k, _ := marshalNoEscape(key)
		out.Write(k)
		out.WriteByte(':')
		out.Write(val)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// marshalNoEscape is json.Marshal without HTML escaping, so text such as
// "<name>" is written as node-gamedig writes it. An outer encoder still
// applies its own escaping setting.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
