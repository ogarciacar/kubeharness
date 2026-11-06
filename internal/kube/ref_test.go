package kube

import (
	"strings"
	"testing"
)

func TestParseRef(t *testing.T) {
	got, err := ParseRef("echo", "default")
	if err != nil || got != (Ref{Namespace: "default", Name: "echo"}) || got.String() != "default/echo" {
		t.Fatalf("bare name = %+v %v", got, err)
	}
	got, err = ParseRef("shop/storefront", "other")
	if err != nil || got != (Ref{Namespace: "shop", Name: "storefront"}) || got.String() != "shop/storefront" {
		t.Fatalf("explicit namespace = %+v %v", got, err)
	}
	got, err = ParseRef("shop/echo.example.com", "default")
	if err != nil || got != (Ref{Namespace: "shop", Name: "echo.example.com"}) {
		t.Fatalf("subdomain name = %+v %v", got, err)
	}

	for _, s := range []string{"", "a/b/c", "/echo", "shop/", "/"} {
		got, err := ParseRef(s, "default")
		if err == nil || got != (Ref{}) || !strings.Contains(err.Error(), "want [namespace/]name") || !strings.Contains(err.Error(), `"`+s+`"`) {
			t.Fatalf("ParseRef(%q) = %+v %v, want a form error quoting the input", s, got, err)
		}
	}
	if _, err := ParseRef("echo", ""); err == nil || !strings.Contains(err.Error(), "want [namespace/]name") {
		t.Fatalf("empty default namespace: %v", err)
	}

	got, err = ParseRef("shop.internal/echo", "default")
	if err == nil || got != (Ref{}) || !strings.Contains(err.Error(), `"shop.internal/echo"`) || !strings.Contains(err.Error(), "namespace:") {
		t.Fatalf("dotted namespace = %+v %v", got, err)
	}
	got, err = ParseRef("Echo", "default")
	if err == nil || got != (Ref{}) || !strings.Contains(err.Error(), `"Echo"`) || !strings.Contains(err.Error(), "name:") {
		t.Fatalf("uppercase name = %+v %v", got, err)
	}
}
