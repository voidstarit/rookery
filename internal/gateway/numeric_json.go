package gateway

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// numericJSONMarshaler keeps protobuf JSON semantics for enums and field
// names, but emits 64-bit integers as JSON numbers. The legacy REST API uses
// numbers for IDs and byte counters, so the HTTP gateway must preserve them.
type numericJSONMarshaler struct {
	*runtime.JSONPb
}

func newNumericJSONMarshaler() *numericJSONMarshaler {
	return &numericJSONMarshaler{JSONPb: &runtime.JSONPb{
		MarshalOptions: protojson.MarshalOptions{
			UseProtoNames:   true,
			EmitUnpopulated: false,
		},
		UnmarshalOptions: protojson.UnmarshalOptions{
			DiscardUnknown: false,
		},
	}}
}

func (m *numericJSONMarshaler) Marshal(value interface{}) ([]byte, error) {
	data, err := m.JSONPb.Marshal(value)
	if err != nil {
		return nil, err
	}
	message, ok := value.(proto.Message)
	if !ok {
		return data, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var generic interface{}
	if err := decoder.Decode(&generic); err != nil {
		return data, nil
	}
	return json.Marshal(rewrite64BitFields(message.ProtoReflect(), generic))
}

func (m *numericJSONMarshaler) NewEncoder(writer io.Writer) runtime.Encoder {
	return &numericJSONEncoder{marshaler: m, writer: writer, fallback: m.JSONPb.NewEncoder(writer)}
}

type numericJSONEncoder struct {
	marshaler *numericJSONMarshaler
	writer    io.Writer
	fallback  runtime.Encoder
}

func (e *numericJSONEncoder) Encode(value interface{}) error {
	if _, ok := value.(proto.Message); !ok {
		return e.fallback.Encode(value)
	}
	data, err := e.marshaler.Marshal(value)
	if err != nil {
		return err
	}
	_, err = e.writer.Write(append(data, '\n'))
	return err
}

func rewrite64BitFields(message protoreflect.Message, value interface{}) interface{} {
	object, ok := value.(map[string]interface{})
	if !ok {
		return value
	}
	fields := message.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		key := string(field.Name())
		raw, present := object[key]
		if !present {
			continue
		}
		if field.IsList() {
			items, ok := raw.([]interface{})
			if !ok {
				continue
			}
			list := message.Get(field).List()
			for index, item := range items {
				if index < list.Len() {
					items[index] = rewriteField(field, item, list.Get(index))
				}
			}
			object[key] = items
			continue
		}
		object[key] = rewriteField(field, raw, message.Get(field))
	}
	return object
}

func rewriteField(field protoreflect.FieldDescriptor, value interface{}, protoValue protoreflect.Value) interface{} {
	switch field.Kind() {
	case protoreflect.Int64Kind, protoreflect.Sint64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		if text, ok := value.(string); ok {
			return json.Number(text)
		}
		return value
	case protoreflect.MessageKind, protoreflect.GroupKind:
		if protoValue.IsValid() && !protoValue.Message().IsValid() {
			return value
		}
		return rewrite64BitFields(protoValue.Message(), value)
	default:
		return value
	}
}
