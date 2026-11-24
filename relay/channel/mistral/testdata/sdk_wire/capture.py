"""Record the requests Mistral Vibe's SDK writes, as fixtures for mistral_test.go.

Vibe 3.1.4 (mistralai/mistral-vibe@e76235b203b8965af584e88e69284bc704c31a1f)
pins mistralai 3.5.0 and httpx2 2.16.0 in uv.lock and runs on CPython 3.12
(.python-version, the release builds). In a virtual environment with just
those two packages and the versions uv.lock pins for their dependencies:

    python3.12 -I capture.py <this directory>

The SDK is called the way Vibe's harness calls it (harness/.../vibe/adapters/
mistral.py, execute_mistral_completion) against a server on 127.0.0.1 that
records each request. Each fixture holds the client's OpenAI request the
gateway receives and the request the SDK writes for the same conversation:
its path, its headers but Host, Content-Length and Authorization, and its
body byte for byte.
"""

import asyncio
import json
import socket
import sys
import types
from pathlib import Path

# Vibe depends on brotli on CPython (pyproject.toml), and httpx2 offers br in
# Accept-Encoding when brotli imports. A stand-in module gives the same header
# without installing it; nothing here decodes a brotli body.
sys.modules.setdefault("brotli", types.ModuleType("brotli"))

import httpx2  # noqa: E402
from mistralai.client import Mistral  # noqa: E402
from mistralai.client.utils.retries import BackoffStrategy, RetryConfig  # noqa: E402

OUT = Path(sys.argv[1])
API_KEY = "mistral-key"
USER_AGENT = "mistral-client-python/Mistral-Vibe/3.1.4"
AFFINITY = "11111111-2222-3333-4444-555555555555"
LEFT_OUT = {"host", "content-length", "authorization"}

STREAM_REPLY = (
    b'data: {"id":"c1","object":"chat.completion.chunk","created":1764000000,"model":"m",'
    b'"choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],'
    b'"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}\n\ndata: [DONE]\n\n'
)
CHAT_REPLY = (
    b'{"id":"c1","object":"chat.completion","created":1764000000,"model":"m","choices":[{"index":0,'
    b'"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],'
    b'"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}'
)
EMBED_REPLY = (
    b'{"id":"e1","object":"list","data":[{"object":"embedding","embedding":[0.1],"index":0}],'
    b'"model":"mistral-embed","usage":{"prompt_tokens":1,"total_tokens":1,"completion_tokens":0}}'
)

captured: list[dict] = []


async def record(reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
    try:
        while True:
            head = (await reader.readuntil(b"\r\n\r\n")).decode("latin-1").split("\r\n")
            method, path, _ = head[0].split(" ")
            headers = [line.split(":", 1) for line in head[1:] if line]
            headers = [[name, value.strip()] for name, value in headers]
            length = next((int(value) for name, value in headers if name.lower() == "content-length"), 0)
            body = (await reader.readexactly(length)).decode("utf-8")
            captured.append({
                "method": method,
                "path": path,
                "headers": [pair for pair in headers if pair[0].lower() not in LEFT_OUT],
                "body": body,
            })
            if path == "/v1/embeddings":
                reply, kind = EMBED_REPLY, b"application/json"
            elif '"stream":false' in body:
                reply, kind = CHAT_REPLY, b"application/json"
            else:
                reply, kind = STREAM_REPLY, b"text/event-stream"
            writer.write(b"HTTP/1.1 200 OK\r\ncontent-type: " + kind + b"\r\ncontent-length: "
                         + str(len(reply)).encode() + b"\r\n\r\n" + reply)
            await writer.drain()
    except (asyncio.IncompleteReadError, ConnectionResetError):
        pass
    finally:
        writer.close()


def tool(name: str, description: str, parameters: dict) -> dict:
    # _tool_payload in adapters/mistral.py; the OpenAI tool a client sends
    # has the same shape.
    return {"type": "function", "function": {"name": name, "description": description, "parameters": parameters}}


READ_FILE = tool("read_file", "Read a file from the workspace.", {
    "type": "object",
    "properties": {"path": {"type": "string", "description": "File path"}, "limit": {"type": "integer", "minimum": 1}},
    "required": ["path"],
})
BASH = tool("bash", "Run a shell command.", {
    "type": "object",
    "properties": {"command": {"type": "string"}, "timeout": {"type": "number", "default": 30.0}},
    "required": ["command"],
})
PNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
TEXT = "Write a title for: café 中文 <b>&amp; \"quotes\" \\ 😀 \u2028 \x7f"


def call(id_: str, name: str, arguments: str) -> dict:
    return {"id": id_, "type": "function", "function": {"name": name, "arguments": arguments}}


# Each case: the arguments Vibe's adapter passes to chat.stream_async (model,
# messages as _message_payload writes them, temperature, reasoning_effort,
# top_p, max_tokens, tools and parallel_tool_calls), and the OpenAI request
# for the same conversation. Vibe sends none of the members of the last two
# chat cases; they record how the SDK writes them.
CASES = [
    ("chat_text", {
        "model": "mistral-large-2512",
        "messages": [{"role": "user", "content": "Hello there"}],
        "temperature": 1.0,
    }, {
        "model": "mistral-large-2512", "stream": True,
        "messages": [{"role": "user", "content": "Hello there"}],
    }),
    ("chat_system", {
        "model": "mistral-large-2512",
        "messages": [
            {"role": "system", "content": "You are Mistral Vibe, a coding agent."},
            {"role": "user", "content": "List the files."},
        ],
        "temperature": 0.2,
    }, {
        "model": "mistral-large-2512", "stream": True, "temperature": 0.2,
        "messages": [
            {"role": "system", "content": "You are Mistral Vibe, a coding agent."},
            {"role": "user", "content": "List the files."},
        ],
    }),
    ("chat_tools", {
        "model": "mistral-vibe-cli-latest",
        "messages": [{"role": "system", "content": "You are Mistral Vibe."}, {"role": "user", "content": "Read a.txt"}],
        "temperature": 1.0,
        "reasoning_effort": "high",
        "tools": [READ_FILE, BASH],
        "parallel_tool_calls": True,
    }, {
        "model": "mistral-vibe-cli-latest", "stream": True, "stream_options": {"include_usage": True},
        "messages": [{"role": "system", "content": "You are Mistral Vibe."}, {"role": "user", "content": "Read a.txt"}],
        "tools": [READ_FILE, BASH],
    }),
    ("chat_tool_rounds", {
        "model": "mistral-large-2512",
        "messages": [
            {"role": "system", "content": "You are Mistral Vibe."},
            {"role": "user", "content": "Read a.txt and b.txt"},
            {"role": "assistant", "content": None, "tool_calls": [
                call("Ab3dE6gH9", "read_file", '{"path":"a.txt"}'), call("Zx8yW7vU6", "read_file", '{"path":"b.txt"}')]},
            {"role": "tool", "tool_call_id": "Ab3dE6gH9", "name": "read_file", "content": "alpha"},
            {"role": "tool", "tool_call_id": "Zx8yW7vU6", "name": "read_file", "content": "beta"},
            {"role": "assistant", "content": "Both files read.", "tool_calls": [call("Qr5sT4uV3", "bash", '{"command":"ls"}')]},
            {"role": "tool", "tool_call_id": "Qr5sT4uV3", "name": "bash", "content": "a.txt\nb.txt"},
            {"role": "user", "content": "Thanks"},
        ],
        "temperature": 1.0,
        "tools": [READ_FILE, BASH],
        "parallel_tool_calls": True,
    }, {
        "model": "mistral-large-2512", "stream": True,
        "messages": [
            {"role": "system", "content": "You are Mistral Vibe."},
            {"role": "user", "content": "Read a.txt and b.txt"},
            {"role": "assistant", "content": None, "tool_calls": [
                call("Ab3dE6gH9", "read_file", '{"path":"a.txt"}'), call("Zx8yW7vU6", "read_file", '{"path":"b.txt"}')]},
            {"role": "tool", "tool_call_id": "Ab3dE6gH9", "content": "alpha"},
            {"role": "tool", "tool_call_id": "Zx8yW7vU6", "content": "beta"},
            {"role": "assistant", "content": "Both files read.", "tool_calls": [call("Qr5sT4uV3", "bash", '{"command":"ls"}')]},
            {"role": "tool", "tool_call_id": "Qr5sT4uV3", "content": "a.txt\nb.txt"},
            {"role": "user", "content": "Thanks"},
        ],
        "tools": [READ_FILE, BASH],
    }),
    ("chat_reasoning_replay", {
        "model": "mistral-vibe-cli-latest",
        "messages": [
            {"role": "user", "content": "What is 2+2?"},
            {"role": "assistant", "content": [
                {"type": "thinking", "thinking": [{"type": "text", "text": "Simple arithmetic."}]},
                {"type": "text", "text": "Let me check."},
            ], "tool_calls": [call("Ab3dE6gH9", "bash", '{"command":"echo 4"}')]},
            {"role": "tool", "tool_call_id": "Ab3dE6gH9", "name": "bash", "content": "4"},
            {"role": "assistant", "content": [{"type": "thinking", "thinking": [{"type": "text", "text": "The tool said 4."}]}]},
            {"role": "user", "content": "And 3+3?"},
        ],
        "temperature": 1.0,
        "reasoning_effort": "high",
        "tools": [BASH],
        "parallel_tool_calls": True,
    }, {
        "model": "mistral-vibe-cli-latest", "stream": True,
        "messages": [
            {"role": "user", "content": "What is 2+2?"},
            {"role": "assistant", "reasoning_content": "Simple arithmetic.", "content": "Let me check.",
             "tool_calls": [call("Ab3dE6gH9", "bash", '{"command":"echo 4"}')]},
            {"role": "tool", "tool_call_id": "Ab3dE6gH9", "content": "4"},
            {"role": "assistant", "reasoning_content": "The tool said 4.", "content": ""},
            {"role": "user", "content": "And 3+3?"},
        ],
        "tools": [BASH],
    }),
    ("chat_image", {
        "model": "mistral-large-2512",
        "messages": [{"role": "user", "content": [
            {"type": "text", "text": "What is in this image?"},
            {"type": "image_url", "image_url": {"url": PNG}},
        ]}],
        "temperature": 1.0,
    }, {
        "model": "mistral-large-2512", "stream": True,
        "messages": [{"role": "user", "content": [
            {"type": "text", "text": "What is in this image?"},
            {"type": "image_url", "image_url": {"url": PNG}},
        ]}],
    }),
    ("chat_effort_max_tokens", {
        "model": "mistral-large-4-0",
        "messages": [{"role": "user", "content": TEXT}],
        "temperature": 0.7,
        "reasoning_effort": "low",
        "top_p": 0.95,
        "max_tokens": 1024,
    }, {
        "model": "mistral-large-4-0", "stream": True, "temperature": 0.7, "top_p": 0.95,
        "max_tokens": 1024, "reasoning_effort": "low",
        "messages": [{"role": "user", "content": TEXT}],
    }),
    ("chat_options", {
        "model": "mistral-small-latest",
        "messages": [{"role": "user", "content": "Return JSON."}],
        "temperature": 0,
        "top_p": 1,
        "stop": ["END"],
        "random_seed": 7,
        "metadata": {"trace": "t-1"},
        "response_format": {"type": "json_schema", "json_schema": {"strict": True, "schema": {"type": "object"}, "name": "answer"}},
        "tools": [READ_FILE],
        "tool_choice": {"type": "function", "function": {"name": "read_file"}},
        "presence_penalty": 0,
        "frequency_penalty": 0.5,
        "n": 2,
        "prediction": {"content": "{}"},
        "parallel_tool_calls": False,
        "prompt_cache_key": "pk-1",
        "safe_prompt": True,
    }, {
        "model": "mistral-small-latest", "stream": True, "temperature": 0, "top_p": 1, "stop": ["END"], "seed": 7,
        "metadata": {"trace": "t-1"},
        "response_format": {"type": "json_schema", "json_schema": {"strict": True, "schema": {"type": "object"}, "name": "answer"}},
        "tools": [READ_FILE], "tool_choice": {"type": "function", "function": {"name": "read_file"}},
        "presence_penalty": 0, "frequency_penalty": 0.5, "n": 2, "prediction": {"content": "{}"},
        "parallel_tool_calls": False, "prompt_cache_key": "pk-1", "safe_prompt": True,
        "messages": [{"role": "user", "content": "Return JSON."}],
    }),
    ("chat_mistral_chunks", {
        "model": "mistral-small-latest",
        "messages": [
            {"role": "user", "content": [
                {"type": "text", "text": "Summarize these."},
                {"type": "image_url", "image_url": {"url": PNG, "detail": "high"}},
                {"type": "document_url", "document_url": "https://example.com/spec.pdf", "document_name": "spec.pdf"},
                {"type": "file", "file_id": "file-1"},
                {"type": "input_audio", "input_audio": "UklGRgAAAAA="},
            ]},
            {"role": "assistant", "content": [
                {"type": "thinking", "thinking": [{"type": "text", "text": "Reading."}, {"type": "reference", "reference_ids": [1, 2]}],
                 "signature": "sig-1", "closed": True},
                {"type": "text", "text": "Done."},
                {"type": "reference", "reference_ids": [3]},
            ]},
            {"role": "user", "content": "More detail."},
        ],
        "temperature": 1.0,
    }, {
        "model": "mistral-small-latest", "stream": True,
        "messages": [
            {"role": "user", "content": [
                {"type": "text", "text": "Summarize these."},
                {"type": "image_url", "image_url": {"detail": "high", "url": PNG}},
                {"document_name": "spec.pdf", "type": "document_url", "document_url": "https://example.com/spec.pdf"},
                {"type": "file", "file": {"file_id": "file-1"}},
                {"type": "input_audio", "input_audio": {"data": "UklGRgAAAAA=", "format": "wav"}},
            ]},
            {"role": "assistant", "content": [
                {"closed": True, "signature": "sig-1", "type": "thinking",
                 "thinking": [{"text": "Reading.", "type": "text"}, {"reference_ids": [1, 2], "type": "reference"}]},
                {"type": "text", "text": "Done."},
                {"reference_ids": [3], "type": "reference"},
            ]},
            {"role": "user", "content": "More detail."},
        ],
    }),
    ("chat_not_streamed", {
        "model": "mistral-large-2512",
        "messages": [{"role": "user", "content": "Hello there"}],
        "temperature": 1.0,
    }, {
        "model": "mistral-large-2512",
        "messages": [{"role": "user", "content": "Hello there"}],
    }),
]

EMBEDDING_CASES = [
    ("embeddings_list", {"model": "mistral-embed", "inputs": ["first text", "second text"]},
     {"model": "mistral-embed", "input": ["first text", "second text"]}),
    ("embeddings_options", {"model": "codestral-embed", "inputs": "one text", "output_dimension": 256, "output_dtype": "int8", "encoding_format": "float"},
     {"model": "codestral-embed", "input": "one text", "dimensions": 256, "output_dtype": "int8", "encoding_format": "float"}),
]


async def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    server = await asyncio.start_server(record, "127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    async with server, httpx2.AsyncClient(
        timeout=httpx2.Timeout(30.0, read=30.0),
        transport=httpx2.AsyncHTTPTransport(trust_env=False, socket_options=[(socket.SOL_SOCKET, socket.SO_KEEPALIVE, 1)]),
        follow_redirects=False,
        headers={"Authorization": f"Bearer {API_KEY}"},
    ) as http_client:
        client = Mistral(
            api_key=API_KEY,
            server_url=f"http://127.0.0.1:{port}",
            async_client=http_client,
            retry_config=RetryConfig(strategy="none", backoff=BackoffStrategy(0, 0, 0, 0), retry_connection_errors=False),
            timeout_ms=30000,
        )
        fixtures = []
        for name, sdk_kwargs, client_request in CASES:
            # Vibe's request_headers(): its User-Agent and the affinity.
            sdk_kwargs = {**sdk_kwargs, "http_headers": {"user-agent": USER_AGENT, "x-affinity": AFFINITY}}
            if client_request.get("stream"):
                async with await client.chat.stream_async(**sdk_kwargs) as events:
                    async for _ in events:
                        pass
            else:
                await client.chat.complete_async(**sdk_kwargs)
            fixtures.append((name, client_request, captured[-1]))
        for name, sdk_kwargs, client_request in EMBEDDING_CASES:
            await client.embeddings.create_async(**sdk_kwargs, http_headers={"user-agent": USER_AGENT})
            fixtures.append((name, client_request, captured[-1]))
    for name, client_request, sdk_request in fixtures:
        fixture = {"client": client_request, "sdk": sdk_request}
        (OUT / f"{name}.json").write_text(json.dumps(fixture, indent=1, ensure_ascii=False) + "\n")
    print(f"wrote {len(fixtures)} fixtures from {len(captured)} requests")


asyncio.run(main())
