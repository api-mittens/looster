package main

import (
	"strings"
	"testing"
)

func TestProtobufLicenseFixture(t *testing.T) {
	name := "protoc-gen-openapiv3/internal/genopenapi/testdata/license_missing_name.prototext"
	if licenseDocument(name) {
		t.Fatal("Protocol fixture is not a license document")
	}
	class, err := noticeClassification(notice{name: name, text: "file_to_generate: bad.proto"})
	if err != nil || !strings.Contains(class, "Supplemental") {
		t.Fatalf("Wrong fixture class: %s %v", class, err)
	}
}
