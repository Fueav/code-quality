package fetch

import "testing"

type body struct{ closed bool }

func (value *body) Close() error {
	value.closed = true
	return nil
}

type transport struct{ body *body }

func (value transport) Fetch() (Response, error) {
	return Response{Body: value.body}, nil
}

func TestFrameworkClosesResponse(t *testing.T) {
	resource := &body{}
	if err := Fetch(transport{body: resource}); err != nil {
		t.Fatal(err)
	}
	if !resource.closed {
		t.Fatal("framework did not close the response")
	}
}
