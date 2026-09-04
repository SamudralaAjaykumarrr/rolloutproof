package ir

import "testing"

func TestShape_HasFieldAndRequiresField(t *testing.T) {
	s, err := NewShape([]Field{
		{Name: "id", Required: true},
		{Name: "note", Required: false},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.HasField("id") || !s.RequiresField("id") {
		t.Fatalf("expected id to be present and required")
	}
	if !s.HasField("note") || s.RequiresField("note") {
		t.Fatalf("expected note to be present and not required")
	}
	if s.HasField("missing") {
		t.Fatalf("expected missing field to be absent")
	}
}

func TestNewShape_RejectsConflictingDuplicate(t *testing.T) {
	_, err := NewShape([]Field{
		{Name: "id", Required: true},
		{Name: "id", Required: false},
	})
	if err == nil {
		t.Fatalf("expected an error for conflicting duplicate field declarations")
	}
}

func TestAPIContract_EndpointLookup(t *testing.T) {
	reqShape, _ := NewShape(nil)
	respShape, _ := NewShape([]Field{{Name: "id", Required: true}, {Name: "total", Required: true}})
	c, err := NewAPIContract("orders-api", "orders", "v2", []Endpoint{
		{Operation: "GET /orders/:id", RequestShape: reqShape, ResponseShape: respShape},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ep, ok := c.Endpoint("GET /orders/:id")
	if !ok {
		t.Fatalf("expected the declared endpoint to be found")
	}
	if !ep.ResponseShape.RequiresField("total") {
		t.Fatalf("expected total to be a required response field")
	}
	if _, ok := c.Endpoint("DELETE /orders/:id"); ok {
		t.Fatalf("expected an undeclared endpoint to be absent")
	}
}

func TestNewAPIContract_RejectsDuplicateOperation(t *testing.T) {
	shape, _ := NewShape(nil)
	_, err := NewAPIContract("orders-api", "orders", "v2", []Endpoint{
		{Operation: "GET /orders/:id", RequestShape: shape, ResponseShape: shape},
		{Operation: "GET /orders/:id", RequestShape: shape, ResponseShape: shape},
	})
	if err == nil {
		t.Fatalf("expected an error for a duplicate operation")
	}
}

func TestNewServiceWithAPI_RoundTrip(t *testing.T) {
	svc, err := NewServiceWithAPI("checkout", "v1", nil, nil, nil,
		[]APIProvision{{ContractName: "checkout-api"}},
		[]APIConsumption{{ContractName: "orders-api", Operation: "GET /orders/:id", RequiredResponseFields: []string{"total"}}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(svc.APIProvides()) != 1 || svc.APIProvides()[0].ContractName != "checkout-api" {
		t.Fatalf("expected APIProvides to round-trip, got %+v", svc.APIProvides())
	}
	if len(svc.APIConsumes()) != 1 || svc.APIConsumes()[0].ContractName != "orders-api" {
		t.Fatalf("expected APIConsumes to round-trip, got %+v", svc.APIConsumes())
	}
}

func TestNewServiceWithAPI_RejectsConsumptionWithoutOperation(t *testing.T) {
	_, err := NewServiceWithAPI("checkout", "v1", nil, nil, nil, nil,
		[]APIConsumption{{ContractName: "orders-api"}},
	)
	if err == nil {
		t.Fatalf("expected an error for an api consumption with no operation")
	}
}
