package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResponseSuccess(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseSuccess[NoMetadata, NoData](req)

	assert.NotNil(t, res)
}

func TestResponseSuccess_SetStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseSuccess[NoMetadata, NoData](req).
		SetStatus(http.StatusOK)

	assert.NotNil(t, res)
}

func TestResponseSuccess_SetMetadata(t *testing.T) {
	type metadata struct {
		Total int `json:"total"`
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseSuccess[metadata, NoData](req).
		SetStatus(http.StatusOK).
		SetMetadata(metadata{Total: 42})

	assert.NotNil(t, res)
}

func TestResponseSuccess_SetData(t *testing.T) {
	type data struct {
		Name string `json:"name"`
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseSuccess[NoMetadata, data](req).
		SetStatus(http.StatusOK).
		SetData(data{Name: "test"})

	assert.NotNil(t, res)
}

func TestResponseSuccess_MarshalJSON(t *testing.T) {
	testcases := []struct {
		name     string
		status   int
		expected string
	}{
		{
			name:     "200 OK",
			status:   http.StatusOK,
			expected: `{"status":"OK"}`,
		},
		{
			name:     "201 Created",
			status:   http.StatusCreated,
			expected: `{"status":"Created"}`,
		},
		{
			name:     "204 No Content",
			status:   http.StatusNoContent,
			expected: `{"status":"No Content"}`,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			res := NewResponseSuccess[NoMetadata, NoData](req).
				SetStatus(tc.status)

			b, err := json.Marshal(res)

			require.NoError(t, err)
			assert.JSONEq(t, tc.expected, string(b))
		})
	}
}

func TestResponseSuccess_MarshalJSON_WithMetadataAndData(t *testing.T) {
	type metadata struct {
		Total int `json:"total"`
	}
	type data struct {
		Name string `json:"name"`
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseSuccess[metadata, data](req).
		SetStatus(http.StatusOK).
		SetMetadata(metadata{Total: 1}).
		SetData(data{Name: "test"})

	b, err := json.Marshal(res)

	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"OK","metadata":{"total":1},"data":{"name":"test"}}`, string(b))
}

func TestResponseSuccess_Write(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()

	NewResponseSuccess[NoMetadata, NoData](req).
		SetStatus(http.StatusOK).
		Write(rw)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"status":"OK"}`, rw.Body.String())
}

func TestResponseSuccess_Write_WithData(t *testing.T) {
	type data struct {
		Id   string `json:"id"`
		Name string `json:"name"`
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()

	NewResponseSuccess[NoMetadata, data](req).
		SetStatus(http.StatusOK).
		SetData(data{Id: "123", Name: "test"}).
		Write(rw)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"status":"OK","data":{"id":"123","name":"test"}}`, rw.Body.String())
}

func TestResponseSuccess_ChainedCalls(t *testing.T) {
	type metadata struct {
		Page  int `json:"page"`
		Total int `json:"total"`
	}
	type item struct {
		Name string `json:"name"`
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseSuccess[metadata, []item](req).
		SetStatus(http.StatusOK).
		SetMetadata(metadata{Page: 1, Total: 2}).
		SetData([]item{{Name: "a"}, {Name: "b"}})

	b, err := json.Marshal(res)

	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"OK","metadata":{"page":1,"total":2},"data":[{"name":"a"},{"name":"b"}]}`, string(b))
}
