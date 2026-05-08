# Claude Desktop Proxy for LiteLLM

A high-performance reverse proxy written in Go that translates model IDs between [Claude Desktop / Claude Code](https://docs.anthropic.com/en/docs/agents-and-tools/claude-code/overview) and [LiteLLM](https://github.com/BerriAI/litellm) (or any OpenAI-compatible endpoint). 

Specifically, this proxy intercepts requests and dynamically maps Anthropic's Claude model IDs to Google's Gemini model IDs, allowing you to seamlessly use Gemini models inside Claude's interfaces.

## Features

- **Blazing Fast**: Written in Go with minimal overhead.
- **In-flight Translation**: Dynamically rewrites incoming `/v1/messages` request payloads to use Gemini IDs.
- **Model Formatting**: Intercepts `/v1/models` responses to format and inject Claude-compatible aliases so they correctly populate in Claude's UI.
- **Production-Ready**: Includes request logging, panic recovery middlewares, and graceful shutdown handling.
- **Easy Configuration**: Uses a simple `config.yaml` file (with CLI flag overrides).

## Prerequisites

- [Go 1.21+](https://golang.org/dl/)

## Installation

Clone the repository and build the binary:

```bash
git clone git@github.com:misternay/claude-desktop-proxy-litellm.git
cd claude-desktop-proxy-litellm

go mod tidy
go build -o proxy
```

## Configuration

Create a `config.yaml` file in the same directory as the executable (or use the provided one):

```yaml
target_url: "https://your-litellm-instance.com"
port: 8080
```

*Note: You can override these values at runtime using command-line flags.*

## Usage

1. Start the proxy server:

```bash
./proxy
```

You can also pass arguments directly:
```bash
./proxy --port 8000 --target https://your-litellm-instance.com
```

## Model Mappings

The proxy automatically performs the following two-way mapping:

| Claude UI ID (Fake) | Real Upstream Model ID (LiteLLM/Gemini) |
| :--- | :--- |
| `claude-2-5-geminiflash-20240101` | `gemini-2.5-flash` |
| `claude-2-5-geminipro-20240101` | `gemini-2.5-pro` |
| `claude-2-5-geminiflashlite-20240101` | `gemini-2.5-flash-lite` |
| `claude-3-1-geminipro-20240101` | `gemini-3.1-pro` |
| `claude-3-0-geminiflash-20240101` | `gemini-3-flash` |

*Other downstream models dynamically get a `claude-3-0-{id}-20240101` alias format.*
