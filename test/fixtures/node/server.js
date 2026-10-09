const http = require("http");
const port = process.env.PORT || 3000;
http
  .createServer((req, res) => {
    res.end(req.url === "/health" ? "ok" : "hello from droidpaas fixture\n");
  })
  .listen(port, process.env.HOST || "127.0.0.1");
