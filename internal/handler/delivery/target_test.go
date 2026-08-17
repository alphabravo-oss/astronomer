package delivery

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func TestPreviewCursorRoundTripAndDigestBinding(t *testing.T) {
	digest := model.Digest("sha256:" + strings.Repeat("a", 64))
	token := encodePreviewCursor(previewCursor{PreviewDigest: digest, Offset: 500})
	request := httptest.NewRequest("POST", "/preview/?page_size=250&cursor="+token, nil)
	pageSize, cursor, err := previewPageRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if pageSize != 250 || cursor == nil || cursor.PreviewDigest != digest || cursor.Offset != 500 {
		t.Fatalf("page = %d cursor = %#v", pageSize, cursor)
	}
}

func TestPreviewCursorRejectsUnboundedOrMalformedInput(t *testing.T) {
	for _, query := range []string{"page_size=0", "page_size=501", "cursor=not-base64", "cursor="} {
		request := httptest.NewRequest("POST", "/preview/?"+query, nil)
		if _, _, err := previewPageRequest(request); err == nil {
			t.Fatalf("query %q was accepted", query)
		}
	}
}
