#!/usr/bin/env python3
import urllib.request
import urllib.error
from http.server import HTTPServer, BaseHTTPRequestHandler
import ssl
import json
import argparse
import sys

# Disable SSL verification for the proxy connection
ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE

# Dictionary to map the faked claude IDs back to real Gemini IDs for the /v1/messages request
ID_MAP = {
    "claude-2-5-geminiflash-20240101": "gemini-2.5-flash",
    "claude-2-5-geminipro-20240101": "gemini-2.5-pro",
    "claude-2-5-geminiflashlite-20240101": "gemini-2.5-flash-lite",
    "claude-3-1-geminipro-20240101": "gemini-3.1-pro",
    "claude-3-0-geminiflash-20240101": "gemini-3-flash"
}

class ProxyHTTPRequestHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        self._handle_request("GET")

    def do_POST(self):
        self._handle_request("POST")

    def _handle_request(self, method):
        url = self.server.target_url + self.path
        
        headers = {}
        for key, value in self.headers.items():
            if key.lower() not in ['host', 'connection', 'accept-encoding']:
                headers[key] = value
                
        data = None
        if 'Content-Length' in self.headers:
            content_length = int(self.headers['Content-Length'])
            data = self.rfile.read(content_length)
            
            # If Claude is sending a completion request, intercept the model ID and switch it back
            if "/v1/messages" in self.path and data:
                try:
                    req_json = json.loads(data.decode('utf-8'))
                    fake_id = req_json.get("model", "")
                    
                    if fake_id in ID_MAP:
                        req_json["model"] = ID_MAP[fake_id]
                        print(f"🔄 Swapped model ID for completion: {fake_id} -> {ID_MAP[fake_id]}", flush=True)
                        data = json.dumps(req_json).encode('utf-8')
                        headers['Content-Length'] = str(len(data))
                except Exception as e:
                    print(f"Failed to swap model ID on incoming request: {e}", flush=True)

        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        
        try:
            with urllib.request.urlopen(req, context=ctx) as response:
                status = response.status
                resp_headers = {}
                for key, value in response.headers.items():
                    if key.lower() not in ['transfer-encoding', 'content-length']:
                        resp_headers[key] = value
                
                resp_data = response.read()
                
                # INTERCEPT AND MODIFY /v1/models RESPONSE
                if "/v1/models" in self.path and status == 200:
                    try:
                        json_data = json.loads(resp_data.decode('utf-8'))
                        anthropic_data = []
                        
                        if "data" in json_data:
                            for model in json_data["data"]:
                                original_id = model.get("id", "")
                                
                                # Use exact mapping format for UI parsing
                                if "gemini-2.5-flash-lite" in original_id:
                                    new_id = "claude-2-5-geminiflashlite-20240101"
                                elif "gemini-2.5-flash" in original_id:
                                    new_id = "claude-2-5-geminiflash-20240101"
                                elif "gemini-2.5-pro" in original_id:
                                    new_id = "claude-2-5-geminipro-20240101"
                                elif "gemini-3.1-pro" in original_id:
                                    new_id = "claude-3-1-geminipro-20240101"
                                elif "gemini-3-flash" in original_id:
                                    new_id = "claude-3-0-geminiflash-20240101"
                                else:
                                    new_id = f"claude-3-0-{original_id.replace('-', '')}-20240101"
                                
                                anthropic_data.append({
                                    "type": "model",
                                    "id": new_id,
                                    "display_name": f"{original_id.title()}",
                                    "created_at": "2024-01-01T00:00:00Z"
                                })
                        
                        modified_response = {"data": anthropic_data, "has_more": False}
                        resp_data = json.dumps(modified_response).encode('utf-8')
                        resp_headers['Content-Type'] = 'application/json'
                        print(f"✅ Served formatted models to Claude UI!", flush=True)
                    except Exception as e:
                        print(f"Failed to transform JSON: {e}", flush=True)

                self.send_response(status)
                for key, value in resp_headers.items():
                    self.send_header(key, value)
                self.send_header('Content-Length', str(len(resp_data)))
                self.end_headers()
                self.wfile.write(resp_data)
                
        except urllib.error.HTTPError as e:
            self.send_response(e.code)
            for key, value in e.headers.items():
                if key.lower() not in ['transfer-encoding']:
                    self.send_header(key, value)
            self.end_headers()
            self.wfile.write(e.read())
            print(f"❌ Error {e.code} on {self.path}", flush=True)
        except Exception as e:
            self.send_response(500)
            self.end_headers()
            print(f"❌ Proxy error: {e}", flush=True)

    def log_message(self, format, *args):
        pass

def run_server():
    parser = argparse.ArgumentParser(description='Claude Code to LiteLLM Proxy')
    parser.add_argument('--port', type=int, default=8080, help='Port to listen on')
    parser.add_argument('--target', type=str, default='https://your-litellm-instance.com', 
                        help='Target LiteLLM URL')
    args = parser.parse_args()

    server = HTTPServer(('127.0.0.1', args.port), ProxyHTTPRequestHandler)
    server.target_url = args.target.rstrip('/')
    
    print(f"🚀 Claude Code UI Proxy running at http://127.0.0.1:{args.port}")
    print(f"📡 Forwarding traffic to: {server.target_url}")
    print("\nTo use this with Claude Code, run:")
    print(f"export ANTHROPIC_BASE_URL=http://127.0.0.1:{args.port}/")
    print("claude cowork")
    print("\nWaiting for requests...")
    
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nShutting down proxy...")
        server.server_close()
        sys.exit(0)

if __name__ == '__main__':
    run_server()
