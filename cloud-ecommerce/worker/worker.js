const amqp = require('amqplib');
const logger = require('./logger');

const rabbitmqUrl = process.env.RABBITMQ_URL || 'amqp://guest:guest@rabbitmq:5672';
let connection;
let channel;

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

const processTask = async (msg) => {
  const content = msg.content.toString();
  try {
    const data = JSON.parse(content);
    logger.info(`Started processing task type "${data.type}" for ${data.email}`);
    
    // Simulate CPU and IO task duration (e.g. rendering invoice PDF or sending mock email)
    const duration = data.type === 'invoice' ? 800 : 300;
    await sleep(duration);
    
    logger.info(`Successfully completed task type "${data.type}" for ${data.email} in ${duration}ms`);
    logPerformance(`task_success_${data.type}`);
    channel.ack(msg);
  } catch (err) {
    logger.error(`Error processing task: ${err.message}. Content: ${content}`);
    // Negative acknowledgment, requeue the message
    channel.nack(msg, false, true);
  }
};

const startWorker = async (retries = 10, delay = 5000) => {
  while (retries > 0) {
    try {
      connection = await amqp.connect(rabbitmqUrl);
      
      // Close listener for graceful shutdowns
      connection.on('close', () => {
        logger.warn('Connection to RabbitMQ lost. Attempting recovery...');
        startWorker(10, delay);
      });

      channel = await connection.createChannel();
      await channel.assertQueue('notifications', { durable: true });
      
      // Set prefetch limit (only process 1 task at a time per worker instance)
      // This allows the scheduler to evenly distribute loads or reclaim workers
      await channel.prefetch(1);
      
      logger.info('Celery-style Worker successfully connected to RabbitMQ notifications queue. Awaiting tasks...');
      
      channel.consume('notifications', (msg) => {
        if (msg !== null) {
          processTask(msg);
        }
      });
      break;
    } catch (err) {
      retries -= 1;
      logger.warn(`Worker failed to connect to RabbitMQ. Retries remaining: ${retries}. Retrying in ${delay / 1000}s...`);
      await sleep(delay);
    }
  }
  if (retries === 0) {
    logger.error('Worker failed to recover RabbitMQ connection. Exiting.');
    process.exit(1);
  }
};

// Periodic heartbeat log showing idle state telemetry
setInterval(() => {
  logPerformance('worker_heartbeat_idle');
}, 30000);

startWorker();
