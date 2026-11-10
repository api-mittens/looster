package ducklakefixture

import (
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestObjectLifecycle(t *testing.T) {
	s, err := Start()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	request := func(method, path, body string, headers map[string]string) (*http.Response, []byte) {
		t.Helper()
		r, err := http.NewRequest(method, s.Endpoint()+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res, b
	}
	res, _ := request("PUT", "/bucket/prefix/a", "abcdef", nil)
	if res.StatusCode != 200 || res.Header.Get("ETag") == "" {
		t.Fatal(res.Status)
	}
	res, b := request("HEAD", "/bucket/prefix/a", "", nil)
	if res.ContentLength != 6 || len(b) != 0 {
		t.Fatal("HEAD", res.ContentLength, string(b))
	}
	res, b = request("GET", "/bucket/prefix/a", "", map[string]string{"Range": "bytes=2-4"})
	if res.StatusCode != 206 || string(b) != "cde" || res.Header.Get("Content-Range") != "bytes 2-4/6" {
		t.Fatal("range", res.Status, string(b))
	}
	res, _ = request("GET", "/bucket/prefix/a", "", map[string]string{"Range": "bytes=20-30"})
	if res.StatusCode != 416 {
		t.Fatal("invalid range", res.Status)
	}
	_, b = request("GET", "/bucket?list-type=2&prefix=prefix/", "", nil)
	var list listing
	if err := xml.Unmarshal(b, &list); err != nil || list.KeyCount != 1 || list.Contents[0].Key != "prefix/a" {
		t.Fatal("list", string(b), err)
	}
	_, b = request("POST", "/bucket/multipart?uploads", "", nil)
	var upload struct{ UploadId string }
	if err := xml.Unmarshal(b, &upload); err != nil || upload.UploadId == "" {
		t.Fatal("init multipart", string(b), err)
	}
	res, _ = request("PUT", "/bucket/multipart?uploadId="+upload.UploadId+"&partNumber=1", "part", nil)
	complete := "<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>" + res.Header.Get("ETag") + "</ETag></Part></CompleteMultipartUpload>"
	res, _ = request("POST", "/bucket/multipart?uploadId="+upload.UploadId, complete, nil)
	if res.StatusCode != 200 {
		t.Fatal("complete", res.Status)
	}
	_, b = request("GET", "/bucket/multipart", "", nil)
	if string(b) != "part" {
		t.Fatal("multipart data", string(b))
	}
	res, b = request("POST", "/bucket?delete", "<Delete><Object><Key>multipart</Key></Object></Delete>", nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), "<Deleted><Key>multipart</Key></Deleted>") {
		t.Fatal("batch delete", res.Status, string(b))
	}
	res, _ = request("HEAD", "/bucket/multipart", "", nil)
	if res.StatusCode != 404 {
		t.Fatal("batch deleted object still present")
	}
	_, b = request("POST", "/bucket/aborted?uploads", "", nil)
	if err := xml.Unmarshal(b, &upload); err != nil {
		t.Fatal(err)
	}
	res, _ = request("DELETE", "/bucket/aborted?uploadId="+upload.UploadId, "", nil)
	if res.StatusCode != 204 {
		t.Fatal("abort", res.Status)
	}
	res, _ = request("PUT", "/bucket/aborted?uploadId="+upload.UploadId+"&partNumber=1", "part", nil)
	if res.StatusCode != 404 {
		t.Fatal("aborted upload accepted part", res.Status)
	}
	request("DELETE", "/bucket/prefix/a", "", nil)
	res, _ = request("GET", "/bucket/prefix/a", "", nil)
	if res.StatusCode != 404 {
		t.Fatal("deleted object", res.Status)
	}
}
