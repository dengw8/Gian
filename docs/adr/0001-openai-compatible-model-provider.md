# OpenAI-compatible model provider

The first real model integration for the Agent Service will use an OpenAI Chat Completions-compatible API configured by base URL, API key, and model name. This keeps the first service interoperable with common model gateways while avoiding a broader multi-provider abstraction before the runtime boundaries are proven.
