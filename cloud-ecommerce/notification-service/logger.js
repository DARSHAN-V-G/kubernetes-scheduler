const winston = require('winston');
const morgan = require('morgan');

const logger = winston.createLogger({
  level: 'info',
  format: winston.format.combine(
    winston.format.timestamp(),
    winston.format.json()
  ),
  defaultMeta: { service: process.env.SERVICE_NAME || 'notification-service' },
  transports: [
    new winston.transports.Console()
  ]
});

const morganMiddleware = morgan((tokens, req, res) => {
  const memUsage = Math.round(process.memoryUsage().rss / 1024 / 1024) + 'MB';
  const cpu = process.cpuUsage();
  const cpuPercent = ((cpu.user + cpu.system) / 1000000).toFixed(2) + 's';

  logger.info({
    method: tokens.method(req, res),
    endpoint: tokens.url(req, res),
    responseTime: tokens['response-time'](req, res) + 'ms',
    statusCode: tokens.status(req, res),
    memory: memUsage,
    cpu: cpuPercent
  });
  return null;
});

module.exports = { logger, morganMiddleware };
