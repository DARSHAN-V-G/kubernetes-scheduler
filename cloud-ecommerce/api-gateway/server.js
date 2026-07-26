const express = require('express');
const { createProxyMiddleware } = require('http-proxy-middleware');
const jwt = require('jsonwebtoken');
const { logger, morganMiddleware } = require('./logger');

const app = express();
const PORT = process.env.PORT || 3000;
const JWT_SECRET = process.env.JWT_SECRET || 'super-secret-key';

app.use(morganMiddleware);

// Health check endpoint
app.get('/api/health', (req, res) => {
  res.json({ status: 'UP', service: 'api-gateway' });
});

// Helper JWT Authentication Middleware
const authenticateToken = (req, res, next) => {
  const authHeader = req.headers['authorization'];
  const token = authHeader && authHeader.split(' ')[1];

  if (!token) return res.status(401).json({ message: 'Missing Authorization Token' });

  jwt.verify(token, JWT_SECRET, (err, user) => {
    if (err) return res.status(403).json({ message: 'Invalid Token' });
    req.user = user;
    next();
  });
};

// Target endpoints for downstream microservices
const USER_SERVICE_URL = process.env.USER_SERVICE_URL || 'http://user-service:3000';
const PRODUCT_SERVICE_URL = process.env.PRODUCT_SERVICE_URL || 'http://product-service:3000';
const ORDER_SERVICE_URL = process.env.ORDER_SERVICE_URL || 'http://order-service:3000';
const NOTIFICATION_SERVICE_URL = process.env.NOTIFICATION_SERVICE_URL || 'http://notification-service:3000';

// Apply JWT authentication specifically for order placements and status checks
app.use('/api/orders', authenticateToken, createProxyMiddleware({
  target: ORDER_SERVICE_URL,
  changeOrigin: true,
  onProxyReq: (proxyReq, req, res) => {
    // Pass decoded user context as HTTP Headers to downstream Order Service
    if (req.user) {
      proxyReq.setHeader('X-User-Id', req.user.id || '1');
      proxyReq.setHeader('X-User-Username', req.user.username || 'guest');
    }
  }
}));

// Route other services directly (User Registration / Catalog search don't require JWT gateway blocking)
app.use('/api/users', createProxyMiddleware({ target: USER_SERVICE_URL, changeOrigin: true }));
app.use('/api/products', createProxyMiddleware({ target: PRODUCT_SERVICE_URL, changeOrigin: true }));
app.use('/api/notifications', createProxyMiddleware({ target: NOTIFICATION_SERVICE_URL, changeOrigin: true }));

app.listen(PORT, () => {
  logger.info(`API Gateway listening on port ${PORT}`);
});
