const http = require('http');

http.createServer((req, res) => {
  res.end(`${process.env.GREETING} from node\n`);
}).listen(3000);
