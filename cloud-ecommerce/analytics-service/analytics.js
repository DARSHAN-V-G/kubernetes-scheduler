const { Kafka } = require('kafkajs');
const logger = require('./logger');

const kafkaBrokers = (process.env.KAFKA_BROKERS || 'kafka:9092').split(',');
const kafka = new Kafka({
  clientId: 'analytics-service',
  brokers: kafkaBrokers,
  retry: {
    initialRetryTime: 300,
    retries: 10
  }
});

const consumer = kafka.consumer({ groupId: 'analytics-group' });

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

const run = async (retries = 10, delay = 5000) => {
  let kafkaConnected = false;
  
  while (retries > 0) {
    try {
      await consumer.connect();
      logger.info('Analytics Service connected to Kafka Broker successfully.');
      kafkaConnected = true;
      break;
    } catch (err) {
      retries -= 1;
      logger.warn(`Kafka connection failed. Retries remaining: ${retries}. Retrying in ${delay / 1000}s...`);
      await new Promise(res => setTimeout(res, delay));
    }
  }

  if (!kafkaConnected) {
    logger.error('Analytics Service failed to connect to Kafka. Exiting.');
    process.exit(1);
  }

  try {
    await consumer.subscribe({ topic: 'orders', fromBeginning: true });
    logger.info('Subscribed to "orders" topic.');

    await consumer.run({
      eachMessage: async ({ topic, partition, message }) => {
        try {
          const payload = JSON.parse(message.value.toString());
          totalSalesRevenue += parseFloat(payload.total || 0);
          totalOrderCount += 1;
          
          logger.info(`Processed analytics for Order ID: ${payload.orderId}. Value: $${payload.total}`);
          logPerformance('analytics_processed_order');
        } catch (e) {
          logger.error(`Error parsing message: ${e.message}`);
        }
      },
    });
  } catch (err) {
    logger.error(`Analytics runtime subscription error: ${err.message}`);
  }
};

// Periodic heartbeat log showing idle state metrics
setInterval(() => {
  logPerformance('analytics_heartbeat_idle');
}, 30000);

run();
