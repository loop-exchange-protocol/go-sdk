package helper

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/loop-exchange-protocol/lxp/pkg/extension"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

type request struct {
	Protocol string          `json:"protocol"`
	ID       uint64          `json:"id"`
	Method   string          `json:"method"`
	Deadline string          `json:"deadline,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
}

type response struct {
	Protocol string          `json:"protocol"`
	ID       uint64          `json:"id"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type initializeParams struct {
	Root           string        `json:"root"`
	ExtensionKind  string        `json:"extension_kind"`
	Contract       spec.Contract `json:"contract"`
	Implementation spec.Contract `json:"implementation"`
}

type initializeResult struct {
	Protocol       string        `json:"protocol"`
	ExtensionKind  string        `json:"extension_kind"`
	Contract       spec.Contract `json:"contract"`
	Implementation spec.Contract `json:"implementation"`
	Distributions  []string      `json:"distributions,omitempty"`
	Capabilities   []string      `json:"capabilities,omitempty"`
}

func protocolName() string { return extension.HelperProtocol }

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("expected exactly one JSON value")
		}
		return err
	}
	return nil
}
