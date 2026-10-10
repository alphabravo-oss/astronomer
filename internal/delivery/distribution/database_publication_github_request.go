package distribution

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// DatabasePublicationGitHubRequest is a captured, nonpersisted executable
// bootstrap input. It cannot select a key, provider, trust policy or registry.
// Values remain private and formatting never exposes the request credential.
type DatabasePublicationGitHubRequest struct{ requestURL, requestToken string }

func CaptureDatabasePublicationGitHubRequest(requestURL, requestToken string) (*DatabasePublicationGitHubRequest, error) {
	if !validDatabasePublicationGitHubRequest(requestURL, requestToken) {
		return nil, errors.New("database publisher protected GitHub request invalid")
	}
	return &DatabasePublicationGitHubRequest{requestURL: requestURL, requestToken: requestToken}, nil
}
func (DatabasePublicationGitHubRequest) String() string {
	return "protected GitHub publication request"
}
func (DatabasePublicationGitHubRequest) GoString() string {
	return "protected GitHub publication request"
}

// A value receiver also protects ordinary dereferenced copies, including a
// mismatched formatting verb. Neither flags nor precision expose raw fields.
func (DatabasePublicationGitHubRequest) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "protected GitHub publication request")
}

func validDatabasePublicationGitHubRequest(requestURL, requestToken string) bool {
	if len(requestURL) == 0 || len(requestURL) > 8192 || len(requestToken) == 0 || len(requestToken) > 16<<10 || strings.ContainsAny(requestURL+requestToken, "\r\n\x00") {
		return false
	}
	location, err := url.Parse(requestURL)
	return err == nil && location.Scheme == "https" && location.Hostname() != "" && location.User == nil && location.Opaque == "" && location.Fragment == "" && location.RawFragment == ""
}

func databasePublicationSignerEnvironment(qa bool, password string, request *DatabasePublicationGitHubRequest) ([]string, error) {
	environment := []string{"PATH=/usr/bin:/bin"}
	if qa {
		return append(environment, "COSIGN_PASSWORD="+password), nil
	}
	if request == nil || !validDatabasePublicationGitHubRequest(request.requestURL, request.requestToken) {
		return nil, errors.New("database publisher protected GitHub request unavailable")
	}
	return append(environment, "ACTIONS_ID_TOKEN_REQUEST_URL="+request.requestURL, "ACTIONS_ID_TOKEN_REQUEST_TOKEN="+request.requestToken), nil
}
