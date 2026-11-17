package gateway

import (
	"context"
	"encoding/json"
	"fmt"
)

// --- tools/call dispatch ---

func (s *Server) handleToolsCall(ctx context.Context, req rpcRequest) rpcResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return rpcErr(req.ID, codeInvalidParams, "invalid tools/call params")
	}
	args := map[string]any{}
	if len(params.Arguments) > 0 {
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			return rpcErr(req.ID, codeInvalidParams, "invalid arguments")
		}
	}

	var (
		result any
		err    error
	)
	switch params.Name {
	case "register":
		result, err = s.toolRegister(ctx, args)
	case "authenticate":
		result, err = s.toolAuthenticate(ctx, args)
	case "send_email":
		result, err = s.toolSend(ctx, args)
	case "read_inbox":
		result, err = s.toolReadInbox(ctx, args)
	case "read_threads":
		result, err = s.toolReadThreads(ctx, args)
	case "read_topic":
		result, err = s.toolReadTopic(ctx, args)
	case "download_attachment":
		result, err = s.toolDownloadAttachment(ctx, args)

	case "get_message":
		result, err = s.toolGetMessage(ctx, args)
	case "wait_for_new_mail":
		result, err = s.toolWaitForNewMail(ctx, args)
	case "duty_watch_guide":
		result = s.toolDutyWatchGuide()
	case "server_info":
		result, err = s.toolServerInfo(ctx, args)
	case "account_info":
		result, err = s.toolAccountInfo(ctx, args)
	case "subs":
		result, err = s.toolSubs(ctx, args)
	case "update_profile":
		result, err = s.toolUpdateProfile(ctx, args)
	default:
		return rpcErr(req.ID, codeMethodNotFound, "unknown tool: "+params.Name)
	}

	if err != nil {
		return toolResult(req.ID, fmt.Sprintf("error: %v", err), true)
	}
	return toolResultJSON(req.ID, result, false)
}

func rpcErr(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func toolResult(id json.RawMessage, text string, isError bool) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}}
}

func toolResultJSON(id json.RawMessage, v any, isError bool) rpcResponse {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return toolResult(id, fmt.Sprintf("(unmarshallable: %v)", err), true)
	}
	return toolResult(id, string(buf), isError)
}
