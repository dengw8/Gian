# Run store port with local file implementation

The Agent Service will persist Run state through a Run Store interface rather than writing directly to a concrete storage backend from the runtime. The first implementation will use local files so the service works without external infrastructure, while keeping the boundary open for later database-backed stores.
