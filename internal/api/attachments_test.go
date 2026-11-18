package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func TestAttachmentUploadValidatesStoresAndServesImage(t *testing.T) {
	state := session.New(8)
	store := session.NewAttachmentObjectStore(t.TempDir())
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	state.SetAttachmentObjectStore(store)
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))

	imageBytes := testPNG(t)
	request := multipartAttachmentRequest(t, "/api/attachments", "files", "截图.png", "image/png", imageBytes)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response attachmentUploadResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	if len(response.Attachments) != 1 {
		t.Fatalf("attachments=%#v", response.Attachments)
	}
	attachment := response.Attachments[0]
	if attachment.Kind != "image" || attachment.MIME != "image/png" || attachment.Width != 2 || attachment.Height != 1 {
		t.Fatalf("unexpected image metadata: %#v", attachment)
	}

	get := httptest.NewRequest(http.MethodGet, "/api/attachments/"+attachment.Hash, nil)
	getRecorder := httptest.NewRecorder()
	server.ServeHTTP(getRecorder, get)
	if getRecorder.Code != http.StatusOK || getRecorder.Header().Get("Content-Type") != "image/png" || !bytes.Equal(getRecorder.Body.Bytes(), imageBytes) {
		t.Fatalf("get mismatch: status=%d type=%q bytes=%d", getRecorder.Code, getRecorder.Header().Get("Content-Type"), getRecorder.Body.Len())
	}
}

func TestAttachmentUploadRejectsDeclaredImageWithInvalidBytes(t *testing.T) {
	state := session.New(8)
	store := session.NewAttachmentObjectStore(t.TempDir())
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	state.SetAttachmentObjectStore(store)
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))
	request := multipartAttachmentRequest(t, "/api/attachments", "files", "fake.png", "image/png", []byte("not an image"))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func multipartAttachmentRequest(t *testing.T, path, field, name, contentType string, payload []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="`+field+`"; filename="`+name+`"`)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write multipart: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{G: 255, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buffer.Bytes()
}
