package api

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const (
	maxAttachmentRequestBytes = 30 << 20
	maxAttachmentFileBytes    = 30 << 20
	maxAttachmentImageBytes   = 10 << 20
	maxAttachmentFiles        = 12
	maxAttachmentImages       = 6
)

type attachmentUploadResponse struct {
	Attachments []schema.AttachmentRef `json:"attachments"`
}

func (s *Server) handleAttachmentCollection(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.runtime == nil {
		http.Error(w, "runtime not configured", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		index, err := s.runtime.AttachmentObjects()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, index)
	case http.MethodPost:
		s.handleAttachmentUpload(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentRequestBytes+1)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		http.Error(w, "附件总大小不能超过 30 MB", http.StatusRequestEntityTooLarge)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	files := multipartAttachmentFiles(r)
	if len(files) == 0 {
		http.Error(w, "请选择要上传的附件", http.StatusBadRequest)
		return
	}
	if len(files) > maxAttachmentFiles {
		http.Error(w, "单次最多上传 12 个附件", http.StatusBadRequest)
		return
	}
	response := attachmentUploadResponse{Attachments: make([]schema.AttachmentRef, 0, len(files))}
	totalBytes := int64(0)
	imageCount := 0
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			http.Error(w, "无法读取附件", http.StatusBadRequest)
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxAttachmentFileBytes+1))
		_ = file.Close()
		if readErr != nil {
			http.Error(w, "无法读取附件", http.StatusBadRequest)
			return
		}
		if len(data) == 0 {
			http.Error(w, "不允许上传空附件", http.StatusBadRequest)
			return
		}
		if len(data) > maxAttachmentFileBytes {
			http.Error(w, "单个附件不能超过 30 MB", http.StatusRequestEntityTooLarge)
			return
		}
		totalBytes += int64(len(data))
		if totalBytes > maxAttachmentRequestBytes {
			http.Error(w, "附件总大小不能超过 30 MB", http.StatusRequestEntityTooLarge)
			return
		}
		attachment, err := inspectAttachment(header.Filename, header.Header.Get("Content-Type"), data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if attachment.Kind == "image" {
			imageCount++
			if imageCount > maxAttachmentImages {
				http.Error(w, "单次最多上传 6 张图片", http.StatusBadRequest)
				return
			}
			if len(data) > maxAttachmentImageBytes {
				http.Error(w, "单张图片不能超过 10 MB", http.StatusRequestEntityTooLarge)
				return
			}
		}
		stored, _, err := s.runtime.PutAttachment(data, attachment)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		response.Attachments = append(response.Attachments, stored)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) handleAttachmentItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	hash := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/attachments/"), "/")
	if hash == "" || s == nil || s.runtime == nil {
		http.NotFound(w, r)
		return
	}
	attachment, data, ok, err := s.runtime.AttachmentObject(hash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if workflowRunQueryBool(r.URL.Query().Get("metadata")) || strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, attachment)
		return
	}
	w.Header().Set("Content-Type", attachment.MIME)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	if disposition := mime.FormatMediaType("inline", map[string]string{"filename": attachment.Name}); disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	_, _ = w.Write(data)
}

func multipartAttachmentFiles(r *http.Request) []*multipart.FileHeader {
	if r == nil || r.MultipartForm == nil {
		return nil
	}
	files := make([]*multipart.FileHeader, 0)
	for _, field := range []string{"files", "attachments", "file"} {
		files = append(files, r.MultipartForm.File[field]...)
	}
	return files
}

func inspectAttachment(name, declaredMIME string, data []byte) (schema.AttachmentRef, error) {
	detectedMIME := http.DetectContentType(data)
	width, height, imageMIME, isImage, err := inspectImage(data)
	if err != nil {
		return schema.AttachmentRef{}, fmt.Errorf("图片内容损坏或格式不受支持: %w", err)
	}
	if isImage {
		return schema.AttachmentRef{
			Name:   filepath.Base(strings.TrimSpace(name)),
			MIME:   imageMIME,
			Kind:   "image",
			Width:  width,
			Height: height,
		}, nil
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(declaredMIME)), "image/") {
		return schema.AttachmentRef{}, fmt.Errorf("附件声明为图片，但实际内容不是受支持的图片")
	}
	return schema.AttachmentRef{
		Name: filepath.Base(strings.TrimSpace(name)),
		MIME: detectedMIME,
		Kind: "file",
	}, nil
}

func (s *Server) resolveTaskAttachments(requested []schema.AttachmentRef) ([]schema.AttachmentRef, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	if len(requested) > maxAttachmentFiles {
		return nil, fmt.Errorf("单次最多使用 12 个附件")
	}
	resolved := make([]schema.AttachmentRef, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	var total int64
	images := 0
	for _, candidate := range requested {
		ref := strings.TrimSpace(candidate.Ref)
		if ref == "" {
			ref = strings.TrimSpace(candidate.Hash)
		}
		attachment, _, ok, err := s.runtime.AttachmentObject(ref)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("附件不存在或已被清理: %s", candidate.Name)
		}
		if _, exists := seen[attachment.Hash]; exists {
			continue
		}
		seen[attachment.Hash] = struct{}{}
		total += attachment.Size
		if total > maxAttachmentRequestBytes {
			return nil, fmt.Errorf("附件总大小不能超过 30 MB")
		}
		if attachment.Kind == "image" {
			images++
			if images > maxAttachmentImages {
				return nil, fmt.Errorf("单次最多使用 6 张图片")
			}
		}
		resolved = append(resolved, attachment)
	}
	return resolved, nil
}

func inspectImage(data []byte) (int, int, string, bool, error) {
	if isWebP(data) {
		width, height, err := webPDimensions(data)
		return width, height, "image/webp", true, err
	}
	detected := http.DetectContentType(data)
	if detected != "image/png" && detected != "image/jpeg" && detected != "image/gif" {
		return 0, 0, "", false, nil
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, "", true, err
	}
	mimeType := "image/" + format
	if format == "jpg" {
		mimeType = "image/jpeg"
	}
	return config.Width, config.Height, mimeType, true, nil
}

func isWebP(data []byte) bool {
	return len(data) >= 16 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP"
}

func webPDimensions(data []byte) (int, int, error) {
	for offset := 12; offset+8 <= len(data); {
		chunkType := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := offset + 8
		end := start + chunkSize
		if chunkSize < 0 || end > len(data) {
			return 0, 0, fmt.Errorf("invalid WebP chunk")
		}
		switch chunkType {
		case "VP8X":
			if chunkSize < 10 {
				return 0, 0, fmt.Errorf("invalid VP8X header")
			}
			width := 1 + int(data[start+4]) + int(data[start+5])<<8 + int(data[start+6])<<16
			height := 1 + int(data[start+7]) + int(data[start+8])<<8 + int(data[start+9])<<16
			return width, height, nil
		case "VP8L":
			if chunkSize < 5 || data[start] != 0x2f {
				return 0, 0, fmt.Errorf("invalid VP8L header")
			}
			bits := binary.LittleEndian.Uint32(data[start+1 : start+5])
			return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1, nil
		case "VP8 ":
			if chunkSize < 10 || !bytes.Equal(data[start+3:start+6], []byte{0x9d, 0x01, 0x2a}) {
				return 0, 0, fmt.Errorf("invalid VP8 header")
			}
			width := int(binary.LittleEndian.Uint16(data[start+6:start+8]) & 0x3fff)
			height := int(binary.LittleEndian.Uint16(data[start+8:start+10]) & 0x3fff)
			return width, height, nil
		}
		offset = end + chunkSize%2
	}
	return 0, 0, fmt.Errorf("WebP image data not found")
}
