package gateway

// toolDef describes one MCP tool for tools/list.
type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func (s *Server) handleToolsList(req rpcRequest) rpcResponse {
	tools := []toolDef{
		{
			Name:        "register",
			Description: "Register a new mail account with a semantic identity. Returns the address and password (if omitted, the gateway creates one and returns it). Store it in session memory; you'll need it to authenticate. Optional server_url targets a different agentmail server than the default.",
			InputSchema: schemaObject(map[string]any{
				"name":       prop("ASCII local-part, e.g. 'frontend-engineer-1'", "string", true),
				"password":   prop("Optional account password (at least 8 characters). If omitted, the gateway generates one and returns it.", "string", false),
				"server_url": prop("Server origin, e.g. http://10.0.0.5:8090. If omitted, uses the default server this gateway started with.", "string", false),
			}, []string{"name"}),
		},
		{
			Name:        "authenticate",
			Description: "Exchange address + password for a short-lived access code. The code is used by send_email/read_inbox/get_message/wait_for_new_mail and expires after a limited time or number of calls. Optional server_url targets a different agentmail server than the default; the access code remembers which server it belongs to, so subsequent calls route automatically.",
			InputSchema: schemaObject(map[string]any{
				"address":    prop("Account address, e.g. 'frontend-engineer-1@agentmail.local'", "string", true),
				"password":   prop("Account password from register", "string", true),
				"server_url": prop("Server origin, e.g. http://10.0.0.5:8090. If omitted, uses the default server this gateway started with.", "string", false),
			}, []string{"address", "password"}),
		},
		{
			Name:        "send_email",
			Description: "Send a plain-text email from the account bound to the access code. Set public=true to additionally publish a copy to the public showcase (portal sample) — an explicit opt-in; delivery is unaffected. inline_files (optional, 1-3 paths) reads plain-text files on the machine where the gateway runs and appends each as a delimited block at the end of the body (100KB per file, 200KB total) — inline text becomes part of the body and never expires. attachments (optional, 1-5 paths) uploads real attachments through the file store — recipients can download them via the codes embedded in the message (1MB per file); attachments are stored server-side with a 30-day TTL and storage caps, so for text you want preserved verbatim prefer inline_files.",
			InputSchema: schemaObject(map[string]any{
				"access_code":  prop("Access code from authenticate", "string", true),
				"to":           arrayProp("Recipient address(es); a comma-separated string is also accepted", true),
				"cc":           arrayProp("Optional carbon-copy address(es), delivered like To and visible to all recipients of the message; a comma-separated string is also accepted", false),
				"in_reply_to":  prop("Optional parent message ULID to reply within a thread; the topic chain is built from these links", "string", false),
				"forward_of":   prop("Optional message_id to forward: the gateway fetches that message as you (it must be in your inbox or sent), prepends your body as the comment, and appends the original below a '── 转发自 ──' header. Attachments of the original are not carried along (a note is added instead). Subject gains an 'Fwd:' prefix unless it already has one.", "string", false),
				"subject":      prop("Subject line", "string", true),
				"body":         prop("Plain-text body", "string", true),
				"public":       prop("Also publish a copy to the public showcase (opt-in, default false)", "boolean", false),
				"inline_files": arrayProp("Optional inline text: 1-3 local file paths whose contents are appended to the body as '--- file: <name> ---' blocks (100KB/file, 200KB total); a comma-separated string is also accepted", false),
				"attachments":  arrayProp("Optional real attachments: 1-5 local file paths uploaded via the file store; recipients receive download codes in the message (1MB/file); a comma-separated string is also accepted", false),
			}, []string{"access_code", "to", "subject", "body"}),
		},
		{
			Name:        "read_inbox",
			Description: "List the most recent messages in the account's inbox bound to the access code. Reads do not consume the access code's call budget (only register/authenticate/send_email do), so this is safe to poll. Pass since_id to receive only messages newer than a known message id (ULIDs are time-ordered, so string comparison filters correctly).",
			InputSchema: schemaObject(map[string]any{
				"access_code": prop("Access code from authenticate", "string", true),
				"limit":       prop("Max messages to return (default 20)", "integer", false),
				"since_id":    prop("Only return messages with id strictly greater than this (for polling without repeats)", "string", false),
			}, []string{"access_code"}),
		},
		{
			Name:        "read_threads",
			Description: "List mail topics (thread chains) for the account bound to the access code: maximal in_reply_to connected components over the account's visible mail, most recently active first. Read-only, does not consume the call budget. limit (default 20), offset (default 0), min_count (default 2 = singletons are not listed; set 1 to include standalone messages).",
			InputSchema: schemaObject(map[string]any{
				"access_code": prop("Access code from authenticate", "string", true),
				"limit":       prop("Max topics to return (default 20)", "number", false),
				"offset":      prop("Pagination offset (default 0)", "number", false),
				"min_count":   prop("Minimum messages per topic (default 2; 1 includes singletons)", "number", false),
			}, []string{"access_code"}),
		},
		{
			Name:        "read_topic",
			Description: "Fetch one whole topic tree in a single call. Pass ANY message id belonging to the conversation — the server resolves it to its connected component and returns every visible message (root = earliest) with in_reply_to links, so an agent that just saw one inbox message can pull the full context without walking the parent chain. Read-only.",
			InputSchema: schemaObject(map[string]any{
				"access_code": prop("Access code from authenticate", "string", true),
				"id":          prop("Any message id in the topic (the root works too; singletons return a one-node tree)", "string", true),
			}, []string{"access_code", "id"}),
		},
		{
			Name:        "get_message",
			Description: "Fetch the full body of a single message by id. Messages with files include an 'attachments' array [{id, filename, size, access_code}] — download each via the download_attachment tool or HTTP GET /api/files/{id}/download?code={access_code} (Basic auth).",
			InputSchema: schemaObject(map[string]any{
				"access_code": prop("Access code from authenticate", "string", true),
				"message_id":  prop("Message id from read_inbox", "string", true),
			}, []string{"access_code", "message_id"}),
		},
		{
			Name:        "download_attachment",
			Description: "Download one attachment by file id. Text files with valid UTF-8 content are returned inline; anything else (binary) comes back base64-encoded with a byte count. Files over 512KB are refused — fetch those via HTTP GET /api/files/{id}/download?code={code} instead. Requires the account to be the file owner or a recipient of the message that carried it (the access_code from that message's attachments entry must also match).",
			InputSchema: schemaObject(map[string]any{
				"access_code": prop("Access code from authenticate", "string", true),
				"file_id":     prop("File id from a message's attachments entry", "string", true),
				"code":        prop("The attachment's access_code from the same attachments entry", "string", true),
			}, []string{"access_code", "file_id", "code"}),
		},
		{
			Name:        "wait_for_new_mail",
			Description: "Block until at least one new message arrives in the account's inbox, or until the timeout elapses (whichever comes first). 'New' means strictly newer than since_id (by ULID time order). The response always includes last_seen_id (the current newest id seen) — pass it as since_id on your next call to maintain a continuous watch without missing in-between mail. If since_id is omitted on the first call, baseline = current newest id. This is a read-only operation: it does not consume the access code's call budget. Default timeout is 25s; there is no internal cap — set it as high as your agent client allows (e.g. 300).",
			InputSchema: schemaObject(map[string]any{
				"access_code": prop("Access code from authenticate", "string", true),
				"since_id":    prop("Only return messages with id strictly greater than this. On subsequent calls, pass the last_seen_id from the previous response to avoid missing mail.", "string", false),
				"timeout":     prop("Max seconds to block waiting (default 25, no internal cap). Returns whatever has arrived when the timeout fires. Pick the largest value your agent client supports.", "integer", false),
			}, []string{"access_code"}),
		},
		{
			Name:        "duty_watch_guide",
			Description: "Return a concise text guide on how to write a reliable inbox watch loop with wait_for_new_mail. No arguments needed. Read this when you are asked to enter a continuous watch/duty/polling mode.",
			InputSchema: schemaObject(map[string]any{}, []string{}),
		},
		{
			Name:        "server_info",
			Description: "Query the server for SYSTEM-level structured information. Pass query to select what to return: \"status\" (version/domain, plus this gateway's own gateway_version and the server's suggested_min_gateway_version — swap the gateway binary when gateway_version is older), \"stats\" (account/message counts), \"settings\" (registration/rate limits), \"accounts\" (full account list, admin only), \"audit\" (recent audit log, admin only), \"help\" (list all queries). Admin-only queries require access_code from an admin account. This tool covers system-wide info; for ACCOUNT-level queries (your own profile, the public directory) use account_info instead. This tool is a thin pass-through — new query types are added on the server side, no gateway change needed.",
			InputSchema: schemaObject(map[string]any{
				"query":       prop("What to query: status, stats, settings, accounts, audit, or help (default: help)", "string", false),
				"access_code": prop("Access code from authenticate. Required for admin-only queries (accounts, audit). Omit for public queries.", "string", false),
			}, []string{}),
		},
		{
			Name:        "account_info",
			Description: "Query ACCOUNT-level information for the account bound to the access code. Pass query to select what to return: \"self\" (your own profile: address, whether you are listed in the directory, and your signature) or \"directory\" (the public address book — every account that opted to be listed, with its signature). Always requires access_code (every query is account-scoped, for a uniform contract). Use this — not server_info — for your own profile or the directory.",
			InputSchema: schemaObject(map[string]any{
				"access_code": prop("Access code from authenticate (required — identifies whose profile / which account)", "string", true),
				"query":       prop("What to query: self (your own profile) or directory (public address book). Default: self.", "string", false),
			}, []string{"access_code"}),
		},
		{
			Name:        "subs",
			Description: "Manage YOUR subordinate relationships (self-declared visibility grants; typical use: an agent account declaring under its owner so the owner can read its mail). action=declare makes YOU a subordinate of superior (idempotent; rate-limited to 10/hour, attachment grants are NOT inherited); action=revoke removes YOUR declaration under superior (takes effect on the next read); action=list returns your edges both ways ({subordinates:[...], superiors:[...]}). Revoking someone else's declaration about you is not possible — each side manages its own.",
			InputSchema: schemaObject(map[string]any{
				"access_code": prop("Access code from authenticate (identifies whose relationships are managed)", "string", true),
				"action":      prop("One of: declare, revoke, list", "string", true),
				"superior":    prop("Superior address (required for declare/revoke, ignored for list), e.g. 'owner@agentmail.local'", "string", false),
			}, []string{"access_code", "action"}),
		},
		{
			Name:        "update_profile",
			Description: "Update YOUR OWN directory profile: whether you are listed in the public address book (visible/listed) and your signature (a short tagline shown next to your address). Requires access_code to identify whose profile to change. The signature is trimmed and capped at 200 characters. Changes take effect immediately in account_info query=directory.",
			InputSchema: schemaObject(map[string]any{
				"access_code": prop("Access code from authenticate (required — identifies the account whose profile is updated)", "string", true),
				"visible":     prop("Whether to list this account in the public directory (true = listed, false = hidden)", "boolean", false),
				"signature":   prop("A short tagline shown next to the address in the directory (trimmed, max 200 chars)", "string", false),
			}, []string{"access_code"}),
		},
	}
	return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools}}
}

func schemaObject(props map[string]any, required []string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func prop(desc, typ string, req bool) map[string]any {
	if req {
		desc += " (required)"
	}
	return map[string]any{"type": typ, "description": desc}
}

// arrayProp declares a string-array parameter. Clients that strictly follow
// the schema rejected array inputs when these were declared "string" (three
// testers tripped on it); a comma-separated single string is still accepted
// by the tolerant parser.
func arrayProp(desc string, req bool) map[string]any {
	if req {
		desc += " (required)"
	}
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}
