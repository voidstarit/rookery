package labels

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeLabels(t *testing.T) {
	got, err := Normalize([]string{" web ", "prod", "web", ""})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"prod", "web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("labels = %#v, want %#v", got, want)
	}
	if _, err := Normalize([]string{"bad,label"}); err == nil {
		t.Fatal("comma label was accepted")
	}
	if _, err := Normalize([]string{strings.Repeat("x", MaxLabelLength+1)}); err == nil {
		t.Fatal("oversized label was accepted")
	}
	many := make([]string, MaxLabels+1)
	for i := range many {
		many[i] = fmt.Sprintf("label-%d", i)
	}
	if _, err := Normalize(many); err == nil {
		t.Fatal("too many labels were accepted")
	}
}

func TestValidEntityType(t *testing.T) {
	for _, entityType := range EntityTypes() {
		if !ValidEntityType(entityType) {
			t.Fatalf("entity type %q is not valid", entityType)
		}
	}
	if ValidEntityType("unknown") {
		t.Fatal("unknown entity type was accepted")
	}
}
