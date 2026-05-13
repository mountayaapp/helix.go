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
		{name: "200 OK", status: http.StatusOK, expected: `{"data":null}`},
		{name: "201 Created", status: http.StatusCreated, expected: `{"data":null}`},
		{name: "204 No Content", status: http.StatusNoContent, expected: `{"data":null}`},
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
	assert.JSONEq(t, `{"data":{"name":"test"},"extensions":{"metadata":{"total":1}}}`, string(b))
}

func TestResponseSuccess_Write(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()

	NewResponseSuccess[NoMetadata, NoData](req).
		SetStatus(http.StatusOK).
		Write(rw)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"data":null}`, rw.Body.String())
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
	assert.JSONEq(t, `{"data":{"id":"123","name":"test"}}`, rw.Body.String())
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
	assert.JSONEq(t, `{"data":[{"name":"a"},{"name":"b"}],"extensions":{"metadata":{"page":1,"total":2}}}`, string(b))
}

func TestResponseSuccess_MarshalJSON_NoMetadataNoData(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseSuccess[NoMetadata, NoData](req).
		SetStatus(http.StatusNoContent)

	b, err := json.Marshal(res)

	require.NoError(t, err)
	assert.JSONEq(t, `{"data":null}`, string(b))
	assert.NotContains(t, string(b), "metadata")
	assert.NotContains(t, string(b), "extensions")
}

func TestResponseSuccess_Write_SetsHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()

	NewResponseSuccess[NoMetadata, NoData](req).
		SetStatus(http.StatusCreated).
		Write(rw)

	assert.Equal(t, http.StatusCreated, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
}

func TestFallbackSuccessResponse_ValidJSON(t *testing.T) {
	var result map[string]any
	err := json.Unmarshal(fallbackSuccessResponse, &result)

	require.NoError(t, err)
	_, ok := result["data"]
	require.True(t, ok, "fallback success response must contain data field per schema invariant")
	assert.Nil(t, result["data"])
}

func TestResponseSuccess_SetData_ComplexType(t *testing.T) {
	type nested struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseSuccess[NoMetadata, nested](req).
		SetStatus(http.StatusOK).
		SetData(nested{
			Items: []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}{
				{ID: "1", Name: "first"},
				{ID: "2", Name: "second"},
			},
		})

	b, err := json.Marshal(res)

	require.NoError(t, err)
	assert.Contains(t, string(b), `"items"`)
	assert.Contains(t, string(b), `"first"`)
	assert.Contains(t, string(b), `"second"`)
}
