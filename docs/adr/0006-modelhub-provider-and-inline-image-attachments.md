# ModelHub Provider and Inline Image Attachments

The Agent Service will support a `modelhub` Model Provider for the ByteDance ModelHub crawl endpoint.

The provider is configured with endpoint, model name, max token limit, and AK. The AK may come from `AGENT_MODELHUB_AK` or from a local `config.yaml` that is ignored by git. `X-TT-LOGID` is generated for each request by default.

User images are represented as inline Message attachments:

```json
{
  "type": "image",
  "media_type": "image/png",
  "data": "...base64 image data...",
  "name": "image.png"
}
```

The Local Store persists attachments in `messages.jsonl` with the rest of the Message. Because image data can make one JSONL entry large, the Local Store reads JSONL with `json.Decoder` instead of `bufio.Scanner`.

The ModelHub Provider maps image attachments to `image_url` content parts using data URLs. This avoids introducing a file upload service, object storage, or attachment lifecycle before there is a clear need for those capabilities.
