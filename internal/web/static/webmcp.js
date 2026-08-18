// WebMCP bridge (W3C document.modelContext): registers the server's MCP
// tools with the browser so an in-page agent can call them. Tools are read
// from the same-origin /mcp/ endpoint at page load, so the registration
// never drifts from what the server actually serves. Every failure is a
// silent no-op — the viewer must work identically without it.
(function () {
    var modelContext = document.modelContext || navigator.modelContext;
    if (!modelContext || typeof modelContext.registerTool !== 'function') {
        return;
    }

    var PROTOCOL = '2026-07-28';
    var nextId = 1;

    // The response to a single-request POST arrives as text/event-stream by
    // default; the JSON-RPC response is the data payload carrying a result
    // or error.
    var parseSSE = function (text) {
        var msg = null;
        text.replace(/\r\n/g, '\n').split('\n\n').forEach(function (event) {
            var data = event.split('\n').filter(function (line) {
                return line.indexOf('data:') === 0;
            }).map(function (line) {
                return line.slice(5).replace(/^ /, '');
            }).join('\n');
            if (!data) {
                return;
            }
            try {
                var parsed = JSON.parse(data);
                if (parsed && (parsed.result !== undefined || parsed.error !== undefined)) {
                    msg = parsed;
                }
            } catch (e) { /* not a JSON-RPC message; skip */ }
        });
        if (!msg) {
            throw new Error('no JSON-RPC response in event stream');
        }
        return msg;
    };

    // rpc POSTs one bare JSON-RPC request. The stateless handler has no
    // initialize handshake, but the 2026-07-28 protocol requires the version
    // and client capabilities in _meta (SEP-2575) and the method mirrored in
    // a header (SEP-2243); tools/call also mirrors the tool name.
    var rpc = function (method, params) {
        var headers = {
            'Content-Type': 'application/json',
            'Accept': 'application/json, text/event-stream',
            'MCP-Protocol-Version': PROTOCOL,
            'Mcp-Method': method
        };
        if (method === 'tools/call') {
            headers['Mcp-Name'] = params.name;
        }
        params = Object.assign({}, params);
        params._meta = {
            'io.modelcontextprotocol/protocolVersion': PROTOCOL,
            'io.modelcontextprotocol/clientCapabilities': {}
        };
        return fetch('/mcp/', {
            method: 'POST',
            headers: headers,
            body: JSON.stringify({ jsonrpc: '2.0', id: nextId++, method: method, params: params })
        }).then(function (resp) {
            if (!resp.ok) {
                // The stateless handler pairs an error status with a JSON-RPC
                // error body whose message names the actual problem (e.g. a
                // header mismatch); surface it when present.
                return resp.text().then(function (text) {
                    var message = '';
                    try {
                        var parsed = JSON.parse(text);
                        if (parsed && parsed.error && parsed.error.message) {
                            message = parsed.error.message;
                        }
                    } catch (e) { /* not JSON; fall through to the generic message */ }
                    throw new Error(message || '/mcp/ returned HTTP ' + resp.status);
                });
            }
            var isSSE = (resp.headers.get('Content-Type') || '').indexOf('text/event-stream') === 0;
            return resp.text().then(function (text) {
                return isSSE ? parseSSE(text) : JSON.parse(text);
            });
        }).then(function (msg) {
            if (msg.error) {
                throw new Error(msg.error.message || 'MCP error ' + msg.error.code);
            }
            return msg.result;
        });
    };

    var register = function (tool) {
        modelContext.registerTool({
            name: tool.name,
            title: tool.title,
            description: tool.description,
            inputSchema: tool.inputSchema,
            // Every tool is a read-only lookup, and specification text is
            // external content, so force both hints whatever the server says.
            annotations: Object.assign({}, tool.annotations, {
                readOnlyHint: true,
                untrustedContentHint: true
            }),
            execute: function (args) {
                return rpc('tools/call', { name: tool.name, arguments: args || {} })
                    .catch(function (err) {
                        // Resolve with an MCP-style error result rather than
                        // throwing, matching how the server reports tool errors.
                        return {
                            content: [{ type: 'text', text: String((err && err.message) || err) }],
                            isError: true
                        };
                    });
            }
        });
    };

    var listPage = function (cursor) {
        return rpc('tools/list', cursor ? { cursor: cursor } : {}).then(function (result) {
            (result.tools || []).forEach(register);
            if (result.nextCursor) {
                return listPage(result.nextCursor);
            }
        });
    };

    listPage().catch(function (err) {
        // /mcp/ may be absent (e.g. stripped by a fronting proxy).
        console.debug('webmcp: tool registration skipped: ' + err);
    });
})();
