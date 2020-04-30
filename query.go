package graphql

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/shurcooL/graphql/ident"
)

type RequestHandlerFunc func(req *http.Request)

type Operation interface {
	Query() string
	Variables() map[string]interface{}
	ResponsePtr() interface{}

	// This will become the `operationName` in the body of the GraphQL request.
	// Since the `operationName` is only required if more than one operation is sent,
	// the tendency should be to return an empty string when there is only one operation in the query.
	OperationName() string

	ModifyRequest(req *http.Request)
}

type Query struct {
	Name string
	Data interface{}
	Vars map[string]interface{}

	RequestHandler RequestHandlerFunc
}

func (op *Query) Query() string {
	var str strings.Builder

	if op.Name != "" {
		str.WriteString("query ")
		str.WriteString(op.Name)
	}

	if len(op.Vars) > 0 {
		if str.Len() == 0 {
			str.WriteString("query")
		}
		str.WriteString("(")
		str.WriteString(queryArguments(op.Vars))
		str.WriteString(")")
	}

	str.WriteString(query(op.Data))

	return str.String()
}

func (op *Query) OperationName() string {
	return "" // we embed the name in the query, not in the request
}

func (op *Query) Variables() map[string]interface{} {
	return op.Vars
}

func (op *Query) ModifyRequest(req *http.Request) {
	if op.RequestHandler != nil {
		op.RequestHandler(req)
	}
}

func (op *Query) ResponsePtr() interface{} {
	return op.Data
}

type Mutation struct {
	Name string
	Data interface{}
	Vars map[string]interface{}

	RequestHandler RequestHandlerFunc
}

func (op *Mutation) Query() string {
	var str strings.Builder

	str.WriteString("mutation")

	if op.Name != "" {
		str.WriteString(" ")
		str.WriteString(op.Name)
	}

	if len(op.Vars) > 0 {
		str.WriteString("(")
		str.WriteString(queryArguments(op.Vars))
		str.WriteString(")")
	}

	str.WriteString(query(op.Data))

	return str.String()
}

func (op *Mutation) OperationName() string {
	return "" // we embed the name in the query, not in the request
}

func (op *Mutation) Variables() map[string]interface{} {
	return op.Vars
}

func (op *Mutation) ModifyRequest(req *http.Request) {
	if op.RequestHandler != nil {
		op.RequestHandler(req)
	}
}

func (op *Mutation) ResponsePtr() interface{} {
	return op.Data
}

type Static struct {
	Name     string
	QueryStr string
	Into     interface{}
	Vars     map[string]interface{}

	RequestHandler RequestHandlerFunc
}

func (op *Static) Query() string {
	return op.QueryStr
}

func (op *Static) OperationName() string {
	return op.Name
}

func (op *Static) Variables() map[string]interface{} {
	return op.Vars
}

func (op *Static) ResponsePtr() interface{} {
	return op.Into
}

func (op *Static) ModifyRequest(req *http.Request) {
	if op.RequestHandler != nil {
		op.RequestHandler(req)
	}
}

// queryArguments constructs a minified arguments string for variables.
//
// E.g., map[string]interface{}{"a": Int(123), "b": NewBoolean(true)} -> "$a:Int!$b:Boolean".
func queryArguments(variables map[string]interface{}) string {
	// Sort keys in order to produce deterministic output for testing purposes.
	// TODO: If tests can be made to work with non-deterministic output, then no need to sort.
	keys := make([]string, 0, len(variables))
	for k := range variables {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	for _, k := range keys {
		io.WriteString(&buf, "$")
		io.WriteString(&buf, k)
		io.WriteString(&buf, ":")
		writeArgumentType(&buf, reflect.TypeOf(variables[k]), true)
		// Don't insert a comma here.
		// Commas in GraphQL are insignificant, and we want minified output.
		// See https://facebook.github.io/graphql/October2016/#sec-Insignificant-Commas.
	}
	return buf.String()
}

// writeArgumentType writes a minified GraphQL type for t to w.
// value indicates whether t is a value (required) type or pointer (optional) type.
// If value is true, then "!" is written at the end of t.
func writeArgumentType(w io.Writer, t reflect.Type, value bool) {
	if t.Kind() == reflect.Ptr {
		// Pointer is an optional type, so no "!" at the end of the pointer's underlying type.
		writeArgumentType(w, t.Elem(), false)
		return
	}

	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		// List. E.g., "[Int]".
		io.WriteString(w, "[")
		writeArgumentType(w, t.Elem(), true)
		io.WriteString(w, "]")
	default:
		// Named type. E.g., "Int".
		name := t.Name()
		if name == "string" { // HACK: Workaround for https://github.com/shurcooL/githubv4/issues/12.
			name = "ID"
		}
		io.WriteString(w, name)
	}

	if value {
		// Value is a required type, so add "!" to the end.
		io.WriteString(w, "!")
	}
}

// query uses writeQuery to recursively construct
// a minified query string from the provided struct v.
//
// E.g., struct{Foo Int, BarBaz *Boolean} -> "{foo,barBaz}".
func query(v interface{}) string {
	var buf bytes.Buffer
	writeQuery(&buf, reflect.TypeOf(v), false)
	return buf.String()
}

// writeQuery writes a minified query for t to w.
// If inline is true, the struct fields of t are inlined into parent struct.
func writeQuery(w io.Writer, t reflect.Type, inline bool) {
	switch t.Kind() {
	case reflect.Ptr, reflect.Slice:
		writeQuery(w, t.Elem(), false)
	case reflect.Struct:
		// If the type implements json.Unmarshaler, it's a scalar. Don't expand it.
		if reflect.PtrTo(t).Implements(jsonUnmarshaler) {
			return
		}
		if !inline {
			io.WriteString(w, "{")
		}
		for i := 0; i < t.NumField(); i++ {
			if i != 0 {
				io.WriteString(w, ",")
			}
			f := t.Field(i)
			value, ok := f.Tag.Lookup("graphql")
			inlineField := f.Anonymous && !ok
			if !inlineField {
				if ok {
					io.WriteString(w, value)
				} else {
					io.WriteString(w, ident.ParseMixedCaps(f.Name).ToLowerCamelCase())
				}
			}
			writeQuery(w, f.Type, inlineField)
		}
		if !inline {
			io.WriteString(w, "}")
		}
	}
}

var jsonUnmarshaler = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
