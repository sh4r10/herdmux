package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// An order-preserving JSON tree. Go's maps lose key order, so objects are a
// member list. Values are *object, []any, string, json.Number, bool or nil.

type member struct {
	key string
	val any
}

type object struct{ members []member }

func (o *object) get(k string) (any, bool) {
	for _, m := range o.members {
		if m.key == k {
			return m.val, true
		}
	}
	return nil, false
}

// set replaces k's value in place, or appends k at the end.
func (o *object) set(k string, v any) {
	for i := range o.members {
		if o.members[i].key == k {
			o.members[i].val = v
			return
		}
	}
	o.members = append(o.members, member{k, v})
}

// del removes every member named k and reports whether there was one.
func (o *object) del(k string) bool {
	out := o.members[:0]
	for _, m := range o.members {
		if m.key != k {
			out = append(out, m)
		}
	}
	found := len(out) != len(o.members)
	o.members = out
	return found
}

// child returns o[k] as an object, creating it at the end when missing. It
// fails when k holds something else.
func (o *object) child(k string) (*object, error) {
	v, ok := o.get(k)
	if !ok {
		c := &object{}
		o.set(k, c)
		return c, nil
	}
	c, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("%q is not an object", k)
	}
	return c, nil
}

// jsonDoc is a parsed JSON file whose top level is an object, with the
// formatting details re-encoding keeps: the indent unit and the final newline.
type jsonDoc struct {
	root    *object
	indent  string
	newline bool
}

// parseDoc parses b (empty or missing means an empty object).
func parseDoc(b []byte) (*jsonDoc, error) {
	d := &jsonDoc{root: &object{}, indent: "  ", newline: true}
	if len(bytes.TrimSpace(b)) == 0 {
		return d, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid JSON: data after the top-level value")
	}
	root, ok := v.(*object)
	if !ok {
		return nil, errors.New("the top level is not a JSON object")
	}
	d.root = root
	d.indent = detectIndent(b)
	d.newline = bytes.HasSuffix(b, []byte("\n"))
	return d, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("invalid JSON: unexpected end")
		}
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := &object{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, fmt.Errorf("invalid JSON: %w", err)
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("invalid JSON: object key is not a string")
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				o.members = append(o.members, member{k, v})
			}
			if _, err := dec.Token(); err != nil {
				return nil, fmt.Errorf("invalid JSON: %w", err)
			}
			return o, nil
		case '[':
			a := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, fmt.Errorf("invalid JSON: %w", err)
			}
			return a, nil
		}
		return nil, fmt.Errorf("invalid JSON: unexpected %v", t)
	default:
		return t, nil
	}
}

// detectIndent returns the leading whitespace of the first indented line,
// or two spaces.
func detectIndent(b []byte) string {
	for _, line := range strings.Split(string(b), "\n")[1:] {
		ws := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if ws != "" && ws != line {
			return ws
		}
	}
	return "  "
}

// encode writes the document in JSON.stringify(v, null, indent) style.
func (d *jsonDoc) encode() []byte {
	var buf bytes.Buffer
	encodeValue(&buf, d.root, d.indent, "")
	if d.newline {
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

func encodeValue(buf *bytes.Buffer, v any, unit, cur string) {
	switch t := v.(type) {
	case *object:
		if len(t.members) == 0 {
			buf.WriteString("{}")
			return
		}
		buf.WriteString("{\n")
		for i, m := range t.members {
			buf.WriteString(cur + unit)
			encodeString(buf, m.key)
			buf.WriteString(": ")
			encodeValue(buf, m.val, unit, cur+unit)
			if i < len(t.members)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(cur + "}")
	case []any:
		if len(t) == 0 {
			buf.WriteString("[]")
			return
		}
		buf.WriteString("[\n")
		for i, e := range t {
			buf.WriteString(cur + unit)
			encodeValue(buf, e, unit, cur+unit)
			if i < len(t)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(cur + "]")
	case string:
		encodeString(buf, t)
	case json.Number:
		buf.WriteString(t.String())
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case nil:
		buf.WriteString("null")
	default:
		panic(fmt.Sprintf("install: unexpected JSON value %T", v))
	}
}

func encodeString(buf *bytes.Buffer, s string) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	buf.Write(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
}

// canonical is a compact encoding, for comparing two values.
func canonical(v any) string {
	var buf bytes.Buffer
	encodeValue(&buf, v, "", "")
	return buf.String()
}

// toTree converts a value parsed by encoding/json (or built in Go) into the
// ordered tree, via a fresh parse of its bytes so key order follows src.
func toTree(src []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	return decodeValue(dec)
}
