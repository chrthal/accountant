package server

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	_ "image/jpeg" // registers the JPEG decoder for image.Decode
	"image/png"
	"io"
	"net/http"
	"strconv"

	"github.com/chrthal/accountant/internal/invoice"
	"github.com/chrthal/accountant/internal/store"
)

// The invoice logo (ADR 0025). It is checked on upload, so a file the PDF
// writer cannot draw never reaches an invoice.

func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) {
	l, ok, err := s.store.GetLogo(r.Context())
	if err != nil {
		s.fail(w, "read logo", err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", l.ContentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(l.Data)
}

func (s *Server) handleLogoUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, store.MaxLogoBytes+64<<10)
	if err := r.ParseMultipartForm(store.MaxLogoBytes); err != nil {
		s.settingsError(w, r, s.T("Das Logo ist zu groß (maximal 1 MiB)."))
		return
	}
	f, _, err := r.FormFile("logo")
	if err != nil {
		s.settingsError(w, r, s.T("Bitte eine Bilddatei auswählen."))
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, store.MaxLogoBytes+1))
	if err != nil {
		s.fail(w, "read logo upload", err)
		return
	}
	contentType, data, err := normaliseLogo(raw)
	if err != nil {
		s.settingsError(w, r, s.T("Das Logo muss ein PNG- oder JPEG-Bild sein."))
		return
	}
	if err := s.store.SaveLogo(r.Context(), contentType, data); err != nil {
		s.settingsError(w, r, s.errText(err))
		return
	}
	http.Redirect(w, r, "/settings?logo=1#invoice-layout", http.StatusSeeOther)
}

func (s *Server) handleLogoDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteLogo(r.Context()); err != nil {
		s.fail(w, "delete logo", err)
		return
	}
	http.Redirect(w, r, "/settings?logo=0#invoice-layout", http.StatusSeeOther)
}

// normaliseLogo accepts PNG and JPEG. A PNG is decoded and written again as
// 8-bit RGBA, because fpdf cannot draw interlaced or 16-bit PNGs. A JPEG is
// kept as it is, since encoding it again would only lose quality.
func normaliseLogo(raw []byte) (string, []byte, error) {
	if len(raw) > store.MaxLogoBytes {
		return "", nil, errors.New("logo too large")
	}
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", nil, err
	}
	b := img.Bounds()
	if b.Dx() > 4000 || b.Dy() > 4000 {
		return "", nil, errors.New("logo has more than 4000 pixels on a side")
	}
	switch format {
	case "jpeg":
		if err := invoice.CheckLogo(invoice.Logo{Data: raw, Type: "JPG"}); err != nil {
			return "", nil, err
		}
		return "image/jpeg", raw, nil
	case "png":
		rgba := image.NewNRGBA(b)
		draw.Draw(rgba, b, img, b.Min, draw.Src)
		var buf bytes.Buffer
		if err := png.Encode(&buf, rgba); err != nil {
			return "", nil, err
		}
		if err := invoice.CheckLogo(invoice.Logo{Data: buf.Bytes(), Type: "PNG"}); err != nil {
			return "", nil, err
		}
		return "image/png", buf.Bytes(), nil
	}
	return "", nil, errors.New("unsupported image format " + strconv.Quote(format))
}
