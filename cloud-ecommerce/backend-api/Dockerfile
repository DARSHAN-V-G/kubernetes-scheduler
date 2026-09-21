FROM node:20-alpine

WORKDIR /usr/src/app

COPY package*.json ./
RUN npm install --production --silent

COPY . .

EXPOSE 3000

CMD ["npm", "start"]
