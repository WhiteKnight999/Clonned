package rst

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

var (
	testHost                     = "127.0.0.1:55839"
	testServerAddr               = "http://" + testHost
	testMux                      *Mux
	testMBText                   []byte
	testSafeURL                  string
	testEchoURL                  string
	testPeople                   []*person
	testPeopleResourceCollection resourceCollection
)

const (
	testContentType   = "hello/world"
	testCannedContent = "hello, world!"
)

var (
	testCannedBytes   = []byte(testCannedContent)
	testTimeReference = time.Date(2014, 4, 14, 10, 0, 0, 0, time.UTC)
)

type employer struct {
	Company   string `json:"company"`
	Continent string `json:"continent"`
}

func (e *employer) LastModified() time.Time {
	return testTimeReference
}

func (e *employer) ETag() string {
	return fmt.Sprintf("%s-%d", e.Company, e.LastModified().Unix())
}

func (e *employer) TTL() time.Duration {
	return 15 * time.Minute
}

func (e *employer) MarshalREST(r *http.Request) (string, []byte, error) {
	accept := ParseAccept(r.Header.Get("Accept"))
	if accept.Negotiate(testContentType) == testContentType {
		return testContentType, testCannedBytes, nil
	}
	return MarshalResource(e, r)
}

type person struct {
	ID        string    `json:"_id"`
	Age       int       `json:"age"`
	EyeColor  string    `json:"eyeColor"`
	Firstname string    `json:"firstname"`
	Lastname  string    `json:"lastname"`
	Employer  *employer `json:"employer"`
}

func (p *person) LastModified() time.Time {
	return testTimeReference
}

func (p *person) ETag() string {
	return fmt.Sprintf("%s-%d", p.ID, p.LastModified().Unix())
}

func (p *person) TTL() time.Duration {
	return 30 * time.Second
}

func (p *person) String() string {
	return fmt.Sprintf("%s %s (%s)", p.Firstname, p.Lastname, p.EyeColor)
}

func (p *person) MarshalREST(r *http.Request) (string, []byte, error) {
	accept := ParseAccept(r.Header.Get("Accept"))
	if accept.Negotiate(testContentType) == testContentType {
		return testContentType, testCannedBytes, nil
	}
	return MarshalResource(p, r)
}

type resourceCollection []Resource

func (c resourceCollection) Count() uint64 {
	return uint64(len(c))
}

func (c resourceCollection) Range(rg *Range) (*ContentRange, Resource, error) {
	if rg.Unit == "unsupported" {
		return nil, nil, ErrUnsupportedRangeUnit
	}
	return &ContentRange{rg, c.Count()}, c[rg.From : rg.To+1], nil
}

func (c resourceCollection) LastModified() time.Time {
	return testTimeReference
}

func (c resourceCollection) ETag() string {
	if len(c) == 0 {
		return "*"
	}
	return c[0].ETag()
}

func (c resourceCollection) TTL() time.Duration {
	return 15 * time.Second
}

func (c resourceCollection) MarshalREST(r *http.Request) (string, []byte, error) {
	accept := ParseAccept(r.Header.Get("Accept"))
	if accept.Negotiate(testContentType) == testContentType {
		return testContentType, testCannedBytes, nil
	}
	return MarshalResource(c, r)
}

type echoResource struct {
	content []byte
}

func (e *echoResource) MarshalREST(r *http.Request) (string, []byte, error) {
	return "text/plain", e.content, nil
}

func (e *echoResource) LastModified() time.Time {
	return testTimeReference
}

func (e *echoResource) ETag() string {
	return "*"
}

func (e *echoResource) TTL() time.Duration {
	return 0
}

type echoEndpoint struct{}

// Post will simply return any data found in the body of the request.
func (ec *echoEndpoint) Post(vars RouteVars, r *http.Request) (Resource, string, error) {
	c, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, "", err
	}
	defer r.Body.Close()
	return &echoResource{c}, "", nil
}

func (ec *echoEndpoint) Preflight(acReq *AccessControlRequest, r *http.Request) *AccessControlResponse {
	return &AccessControlResponse{
		Origin: "preflighted.domain.com",
	}
}

type peopleCollection struct{}

// Get returns the content of testPeople.
func (c *peopleCollection) Get(vars RouteVars, r *http.Request) (Resource, error) {
	col := make(resourceCollection, len(testPeople))
	for i, p := range testPeople {
		col[i] = Resource(p)
	}
	if col.Count() == 0 {
		return nil, nil
	}
	return col, nil
}

// Post returns the first item in testPeople as if it was just created.
func (c *peopleCollection) Post(vars RouteVars, r *http.Request) (Resource, string, error) {
	if r.Header.Get("Content-Type") != "application/json" {
		return nil, "", UnsupportedMediaType("application/json")
	}

	return testPeople[0], "https://", nil
}

type personResource struct{}

func (e *personResource) Get(vars RouteVars, r *http.Request) (Resource, error) {
	for _, p := range testPeople {
		if p.ID == vars.Get("id") {
			return p, nil
		}
	}
	return nil, NotFound()
}

func (e *personResource) Delete(vars RouteVars, r *http.Request) error {
	for i, p := range testPeople {
		if p.ID == vars.Get("id") {
			testPeople = append(testPeople[:i], testPeople[i+1:]...)
			return nil
		}
	}
	return NotFound()
}

type employersCollection struct{}

func (c *employersCollection) Get(vars RouteVars, r *http.Request) (Resource, error) {
	index := make(map[string]Resource)
	for _, p := range testPeople {
		index[p.Employer.Company] = Resource(p.Employer)
	}
	if len(index) == 0 {
		return nil, nil
	}

	col := make(resourceCollection, len(index))
	for _, resource := range index {
		col = append(col, resource)
	}
	return col, nil
}

type employerResource struct{}

func (e *employerResource) Get(vars RouteVars, r *http.Request) (Resource, error) {
	for _, p := range testPeople {
		if strings.EqualFold(p.Employer.Company, vars.Get("name")) {
			return p.Employer, nil
		}
	}
	return nil, NotFound()
}

func TestMain(m *testing.M) {
	var err error

	// 1MB text
	testMBText, err = ioutil.ReadFile("1mb.txt")
	if err != nil {
		log.Fatal(err)
	}

	// DB
	rawdb, err := ioutil.ReadFile("100objects.json")
	if err != nil {
		log.Fatal(err)
	}
	if err := json.Unmarshal(rawdb, &testPeople); err != nil {
		log.Fatal(err)
	}
	for _, p := range testPeople {
		testPeopleResourceCollection = append(testPeopleResourceCollection, Resource(p))
	}

	testMux = NewMux()
	testMux.Handle("/echo", EndpointHandler(&echoEndpoint{}))
	testMux.Handle("/people", EndpointHandler(&peopleCollection{}))
	testMux.Handle("/people/{id}", EndpointHandler(&personResource{}))
	testMux.Handle("/employers", EndpointHandler(&employersCollection{}))
	testMux.Handle("/employers/{name}", EndpointHandler(&employerResource{}))
	go http.ListenAndServe(testHost, testMux)

	testEchoURL = testServerAddr + "/echo"
	testSafeURL = testServerAddr + "/people"

	os.Exit(m.Run())
}
