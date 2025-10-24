package internal

import (
	"fmt"
	"path/filepath"

	"google.golang.org/protobuf/proto"
	rotompb "rotomworker/proto_gen"
)

// ReqHandlerFn manipula um MitmRequest e pode preencher uma MitmResponse
type ReqHandlerFn func(req *rotompb.MitmRequest, resp *rotompb.MitmResponse)

// RespHandlerFn manipula uma MitmResponse recebida
type RespHandlerFn func(resp *rotompb.MitmResponse)

var (
	requestHandlers  = map[string]ReqHandlerFn{}
	responseHandlers = map[string]RespHandlerFn{}
)

// RegisterRequestHandler registra um handler de request por nome (ex: "LOGIN", "RPC_REQUEST")
func RegisterRequestHandler(name string, h ReqHandlerFn) {
	requestHandlers[name] = h
}

// RegisterResponseHandler registra um handler de response por chave (ex: status code)
func RegisterResponseHandler(name string, h RespHandlerFn) {
	responseHandlers[name] = h
}

// DispatchMitmRequest decodifica MitmRequest e chama handler apropriado.
func DispatchMitmRequest(raw []byte) (handled bool, respBytes []byte, err error) {
	var req rotompb.MitmRequest
	if err := proto.Unmarshal(raw, &req); err != nil {
		return false, nil, fmt.Errorf("failed to unmarshal MitmRequest: %w", err)
	}

	methodName := "UNSET"
	switch req.GetMethod() {
	case rotompb.MitmRequest_LOGIN:
		methodName = "LOGIN"
	case rotompb.MitmRequest_RPC_REQUEST:
		methodName = "RPC_REQUEST"
	default:
		methodName = fmt.Sprintf("METHOD_%d", int(req.GetMethod()))
	}

	if h, ok := requestHandlers[methodName]; ok {
		var resp rotompb.MitmResponse
		h(&req, &resp)

		if resp.GetStatus() != rotompb.MitmResponse_UNSET {
			out, perr := proto.Marshal(&resp)
			if perr != nil {
				return true, nil, fmt.Errorf("failed to marshal MitmResponse: %w", perr)
			}
			return true, out, nil
		}
		return true, nil, nil
	}

	// fallback para RPC_REQUEST (inspeciona internamente, se desejar)
	if methodName == "RPC_REQUEST" {
		if rr := req.GetRpcRequest(); rr != nil {
			for _, single := range rr.GetRequest() {
				_ = single.GetMethod()
			}
		}
	}

	return false, nil, nil
}

// DispatchMitmResponse decodifica MitmResponse e chama response handlers.
func DispatchMitmResponse(raw []byte) (handled bool, err error) {
	var resp rotompb.MitmResponse
	if err := proto.Unmarshal(raw, &resp); err != nil {
		return false, fmt.Errorf("failed to unmarshal MitmResponse: %w", err)
	}

	key := fmt.Sprintf("%d", int(resp.GetStatus()))
	if h, ok := responseHandlers[key]; ok {
		h(&resp)
		return true, nil
	}

	if rr := resp.GetRpcResponse(); rr != nil {
		_ = rr
	}
	return false, nil
}

// RegisterDefaultHandlers registra handlers padrão no estilo Cosmog.
func RegisterDefaultHandlers() {
	RegisterRequestHandler("LOGIN", func(req *rotompb.MitmRequest, resp *rotompb.MitmResponse) {
		resp.Status = rotompb.MitmResponse_SUCCESS

		lr := &rotompb.MitmResponse_LoginResponse{}
		if loginReq := req.GetLoginRequest(); loginReq != nil {
			lr.WorkerId = loginReq.GetWorkerId()
		}
		lr.Status = rotompb.AuthStatus_AUTH_STATUS_GOT_AUTH_TOKEN
		lr.SupportsCompression = false
		lr.Useragent = "rotom-worker-go/1.0"

		resp.Payload = &rotompb.MitmResponse_LoginResponse_{
			LoginResponse: lr,
		}
	})

	RegisterRequestHandler("RPC_REQUEST", func(req *rotompb.MitmRequest, resp *rotompb.MitmResponse) {
		resp.Status = rotompb.MitmResponse_SUCCESS
		rr := &rotompb.MitmResponse_RpcResponse{}
		rr.RpcStatus = rotompb.RpcStatus_RPC_STATUS_SUCCESS

		resp.Payload = &rotompb.MitmResponse_RpcResponse_{
			RpcResponse: rr,
		}
	})
}

// DecodePogoPayload tenta interpretar payloads RPC usando POGOProtos.
func DecodePogoPayload(raw []byte) (string, bool) {
	return "", false
}

func basename(p string) string {
	return filepath.Base(p)
}
