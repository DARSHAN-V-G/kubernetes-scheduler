const { createClient } = require('redis');
const logger = require('./logger');

const REDIS_URL = process.env.REDIS_URL || 'redis://redis:6379';
const CHANNEL_NAME = 'orders_stream';

let totalSalesRevenue = 0;
let totalOrderCount = 0;

const logPerformance = (action) => {
  const memUsage = Math.round(process.memoryUsage().rss / 1024 / 1024) + 'MB';
  const cpu = process.cpuUsage();
  const cpuPercent = ((cpu.user + cpu.system) / 1000000).toFixed(2) + 's';

  logger.info({
    action,
    totalSalesRevenue: `$${totalSalesRevenue.toFixed(2)}`,
    totalOrdersProcessed: totalOrderCount,
    memory: memUsage,
    cpu: cpuPercent,
    timestamp: new Date().toISOString()
  });
};

const run = async (retries = 10, delay = 3000) => {
  const subscriber = createClient({ url: REDIS_URL });
  subscriber.on('error', (err) => logger.error(`Redis Subscriber Error: ${err.message}`));

  while (retries > 0) {
    try {
      await subscriber.connect();
      logger.info('Analytics Service connected to Redis successfully.');
      break;
    } catch (err) {
      retries -= 1;
      logger.warn(`Redis connection failed. Retries remaining: ${retries}. Retrying in ${delay / 1000}s...`);
      await new Promise((res) => setTimeout(res, delay));
    }
  }

  if (!subscriber.isOpen) {
    logger.error('Analytics Service failed to connect to Redis. Exiting.');
    process.exit(1);
  }

  try {
    await subscriber.subscribe(CHANNEL_NAME, (message) => {
      try {
        const payload = JSON.parse(message);
        totalSalesRevenue += parseFloat(payload.total || 0);
        totalOrderCount += 1;

        logger.info(`Processed analytics for Order ID: ${payload.orderId}. Value: $${payload.total}`);
        logPerformance('analytics_processed_order');
      } catch (e) {
        logger.error(`Error parsing order event message: ${e.message}`);
      }
    });

    logger.info(`Subscribed to "${CHANNEL_NAME}" channel.`);
  } catch (err) {
    logger.error(`Analytics subscription error: ${err.message}`);
  }
};

// Periodic heartbeat log showing idle state metrics
setInterval(() => {
  logPerformance('analytics_heartbeat_idle');
}, 30000);

run();
