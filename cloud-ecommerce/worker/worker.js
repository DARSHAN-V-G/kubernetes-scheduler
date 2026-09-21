const { createClient } = require('redis');
const logger = require('./logger');

const REDIS_URL = process.env.REDIS_URL || 'redis://redis:6379';
const QUEUE_NAME = 'notifications_queue';

let redisClient;
let isRunning = true;

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const logPerformance = (action) => {
  const memUsage = Math.round(process.memoryUsage().rss / 1024 / 1024) + 'MB';
  const cpu = process.cpuUsage();
  const cpuPercent = ((cpu.user + cpu.system) / 1000000).toFixed(2) + 's';

  logger.info({
    action,
    memory: memUsage,
    cpu: cpuPercent,
    timestamp: new Date().toISOString()
  });
};

const processTask = async (rawTask) => {
  try {
    const data = JSON.parse(rawTask);
    logger.info(`Started processing task type "${data.type}" for ${data.email}`);

    // Simulate CPU and IO task duration (e.g., rendering invoice PDF, generating email)
    const duration = data.type === 'invoice' ? 800 : 300;
    await sleep(duration);

    logger.info(`Successfully completed task type "${data.type}" for ${data.email} in ${duration}ms`);
    logPerformance(`task_success_${data.type}`);
  } catch (err) {
    logger.error(`Error processing task: ${err.message}. Raw: ${rawTask}`);
  }
};

const startWorker = async (retries = 10, delay = 3000) => {
  redisClient = createClient({ url: REDIS_URL });
  redisClient.on('error', (err) => logger.error(`Redis Client Error: ${err.message}`));

  while (retries > 0) {
    try {
      await redisClient.connect();
      logger.info(`Worker connected to Redis successfully. Listening on queue "${QUEUE_NAME}"...`);
      break;
    } catch (err) {
      retries -= 1;
      logger.warn(`Redis connection failed. Retries remaining: ${retries}. Retrying in ${delay / 1000}s...`);
      await sleep(delay);
    }
  }

  if (retries === 0) {
    logger.error('Worker failed to connect to Redis. Exiting.');
    process.exit(1);
  }

  // Work processing loop (blocking pop with 2-second timeout to allow loop checks)
  while (isRunning) {
    try {
      const result = await redisClient.brPop(QUEUE_NAME, 2);
      if (result && result.element) {
        await processTask(result.element);
      }
    } catch (err) {
      if (isRunning) {
        logger.error(`Queue polling error: ${err.message}`);
        await sleep(2000);
      }
    }
  }
};

// Periodic heartbeat log showing idle state telemetry
setInterval(() => {
  logPerformance('worker_heartbeat_idle');
}, 30000);

startWorker();
