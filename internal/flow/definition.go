package flow

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type ordered struct {
	keys []string
	vals map[string]json.RawMessage
}

// ActionNames returns flow action names in definition order.
// Trigger names are not included. Nested scope, condition, and switch actions
// are listed depth-first after their parent.
func ActionNames(flowBody []byte) ([]string, error) {
	root, err := decodeOrdered(flowBody)
	if err != nil {
		return nil, fmt.Errorf("decode flow: %w", err)
	}
	props, ok, err := root.object("properties")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("flow response missing properties")
	}
	defRaw, ok := props.vals["definition"]
	if !ok {
		return nil, fmt.Errorf("flow response missing definition")
	}
	defBody, err := unwrapDefinition(defRaw)
	if err != nil {
		return nil, err
	}
	def, err := decodeOrdered(defBody)
	if err != nil {
		return nil, fmt.Errorf("decode definition: %w", err)
	}

	var names []string
	if triggers, ok, err := def.object("triggers"); err != nil {
		return nil, err
	} else if ok {
		for _, key := range triggers.keys {
			body, ok, err := triggers.object(key)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if err := walkContainer(body, &names); err != nil {
				return nil, err
			}
		}
	}
	if actions, ok, err := def.object("actions"); err != nil {
		return nil, err
	} else if ok {
		if err := walkActions(actions, &names); err != nil {
			return nil, err
		}
	}
	return names, nil
}

func unwrapDefinition(raw json.RawMessage) ([]byte, error) {
	trim := bytes.TrimSpace(raw)
	if len(trim) == 0 || bytes.Equal(trim, []byte("null")) {
		return nil, fmt.Errorf("flow definition is empty")
	}
	if trim[0] != '"' {
		return trim, nil
	}
	var encoded string
	if err := json.Unmarshal(trim, &encoded); err != nil {
		return nil, fmt.Errorf("decode definition string: %w", err)
	}
	return []byte(encoded), nil
}

func walkActions(actions *ordered, names *[]string) error {
	for _, name := range actions.keys {
		*names = append(*names, name)
		body, ok, err := actions.object(name)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := walkContainer(body, names); err != nil {
			return err
		}
	}
	return nil
}

func walkContainer(obj *ordered, names *[]string) error {
	if actions, ok, err := obj.object("actions"); err != nil {
		return err
	} else if ok {
		if err := walkActions(actions, names); err != nil {
			return err
		}
	}
	if els, ok, err := obj.object("else"); err != nil {
		return err
	} else if ok {
		if err := walkContainer(els, names); err != nil {
			return err
		}
	}
	if cases, ok, err := obj.object("cases"); err != nil {
		return err
	} else if ok {
		for _, key := range cases.keys {
			caseObj, ok, err := cases.object(key)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if err := walkContainer(caseObj, names); err != nil {
				return err
			}
		}
	}
	if def, ok, err := obj.object("default"); err != nil {
		return err
	} else if ok {
		if err := walkContainer(def, names); err != nil {
			return err
		}
	}
	return nil
}

func (o *ordered) object(key string) (*ordered, bool, error) {
	raw, ok := o.vals[key]
	if !ok {
		return nil, false, nil
	}
	trim := bytes.TrimSpace(raw)
	if len(trim) == 0 || bytes.Equal(trim, []byte("null")) || trim[0] != '{' {
		return nil, false, nil
	}
	child, err := decodeOrdered(trim)
	if err != nil {
		return nil, false, err
	}
	return child, true, nil
}

func decodeOrdered(raw []byte) (*ordered, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok || d != '{' {
		return nil, fmt.Errorf("expected object")
	}
	return readObject(dec)
}

func readObject(dec *json.Decoder) (*ordered, error) {
	o := &ordered{vals: map[string]json.RawMessage{}}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("expected string key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.keys = append(o.keys, key)
		o.vals[key] = raw
	}
	end, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := end.(json.Delim)
	if !ok || d != '}' {
		return nil, fmt.Errorf("expected end of object")
	}
	return o, nil
}
