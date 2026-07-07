# Embedded Web Console

The Agent Service will serve a static Web Console from `/app/` using assets embedded in the Go binary.

This keeps local operation simple: the API and UI share one process, one port, and same-origin requests. The console is an operator and developer surface for creating Runs, loading persisted Run history, sending Turns, attaching images, and inspecting messages and events.

The console intentionally uses plain HTML, CSS, and JavaScript. There is no Node build step, package manager, or separate frontend service. If the console grows into a larger product surface, a separate frontend build can be introduced later behind the same HTTP route.
