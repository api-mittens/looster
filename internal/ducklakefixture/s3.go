// Package ducklakefixture provides a deliberately unauthenticated, loopback-only
// S3 subset for disposable integration tests. Never use it for application data.
package ducklakefixture

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type object struct {
	data     []byte
	modified time.Time
}
type Server struct {
	mu       sync.Mutex
	objects  map[string]object
	uploads  map[string]map[int][]byte
	next     int
	requests map[string]int
	server   *http.Server
	listener net.Listener
}

// Start binds an ephemeral IPv4 loopback port. Objects live only in memory.
func Start() (*Server, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{objects: map[string]object{}, uploads: map[string]map[int][]byte{}, requests: map[string]int{}, listener: l}
	s.server = &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second}
	go s.server.Serve(l)
	return s, nil
}
func (s *Server) Endpoint() string { return "http://" + s.listener.Addr().String() }
func (s *Server) Close() error     { return s.server.Close() }

// Requests returns a snapshot of protocol operations served by this fixture.
func (s *Server) Requests() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]int, len(s.requests))
	for key, count := range s.requests {
		result[key] = count
	}
	return result
}
func etag(b []byte) string { sum := md5.Sum(b); return `"` + hex.EncodeToString(sum[:]) + `"` }
func xmlReply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(v)
}

type entry struct {
	Key          string
	LastModified string
	ETag         string
	Size         int
}
type listing struct {
	XMLName     xml.Name `xml:"ListBucketResult"`
	Name        string
	Prefix      string
	KeyCount    int
	MaxKeys     int
	IsTruncated bool
	Contents    []entry
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[r.Method]++
	if r.Header.Get("Range") != "" {
		s.requests["RANGE"]++
	}
	key := strings.TrimPrefix(r.URL.Path, "/")
	bucket, path, _ := strings.Cut(key, "/")
	q := r.URL.Query()
	w.Header().Set("x-amz-request-id", "disposable-fixture")
	if r.Method == "GET" && q.Get("list-type") == "2" {
		result := listing{Name: bucket, Prefix: q.Get("prefix"), MaxKeys: 1000}
		keys := []string{}
		for k := range s.objects {
			if strings.HasPrefix(k, bucket+"/"+result.Prefix) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			o := s.objects[k]
			result.Contents = append(result.Contents, entry{Key: strings.TrimPrefix(k, bucket+"/"), LastModified: o.modified.Format(time.RFC3339), ETag: etag(o.data), Size: len(o.data)})
		}
		result.KeyCount = len(result.Contents)
		xmlReply(w, result)
		return
	}
	if r.Method == "POST" && q.Has("uploads") {
		s.next++
		id := strconv.Itoa(s.next)
		s.uploads[id] = map[int][]byte{}
		xmlReply(w, struct {
			XMLName               xml.Name `xml:"InitiateMultipartUploadResult"`
			Bucket, Key, UploadId string
		}{Bucket: bucket, Key: path, UploadId: id})
		return
	}
	if id := q.Get("uploadId"); id != "" {
		parts, ok := s.uploads[id]
		if !ok {
			http.Error(w, "NoSuchUpload", 404)
			return
		}
		switch r.Method {
		case "PUT":
			n, err := strconv.Atoi(q.Get("partNumber"))
			if err != nil {
				http.Error(w, "InvalidPart", 400)
				return
			}
			b, err := io.ReadAll(io.LimitReader(r.Body, 128<<20))
			if err != nil {
				http.Error(w, "read", 400)
				return
			}
			parts[n] = b
			w.Header().Set("ETag", etag(b))
			return
		case "POST":
			var request struct {
				Parts []struct {
					PartNumber int
					ETag       string
				} `xml:"Part"`
			}
			if xml.NewDecoder(r.Body).Decode(&request) != nil || len(request.Parts) == 0 {
				http.Error(w, "MalformedXML", 400)
				return
			}
			var data []byte
			for _, p := range request.Parts {
				b, ok := parts[p.PartNumber]
				if !ok || etag(b) != p.ETag {
					http.Error(w, "InvalidPart", 400)
					return
				}
				data = append(data, b...)
			}
			s.objects[key] = object{data, time.Now().UTC()}
			delete(s.uploads, id)
			xmlReply(w, struct {
				XMLName           xml.Name `xml:"CompleteMultipartUploadResult"`
				Bucket, Key, ETag string
			}{Bucket: bucket, Key: path, ETag: etag(data)})
			return
		case "DELETE":
			delete(s.uploads, id)
			w.WriteHeader(204)
			return
		}
	}
	switch r.Method {
	case "PUT":
		b, err := io.ReadAll(io.LimitReader(r.Body, (128<<20)+1))
		if err != nil || len(b) > 128<<20 {
			http.Error(w, "fixture object limit", 413)
			return
		}
		s.objects[key] = object{b, time.Now().UTC()}
		w.Header().Set("ETag", etag(b))
		w.WriteHeader(200)
	case "HEAD", "GET":
		o, ok := s.objects[key]
		if !ok {
			http.Error(w, "NoSuchKey", 404)
			return
		}
		w.Header().Set("ETag", etag(o.data))
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, path, o.modified, bytes.NewReader(o.data))
	case "DELETE":
		delete(s.objects, key)
		w.WriteHeader(204)
	default:
		http.Error(w, fmt.Sprintf("unsupported fixture operation: %s", r.Method), 501)
	}
}
