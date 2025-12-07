import IORedis from "ioredis";

// Conecta no Redis que subimos no Docker (porta 6379)
export const connection = new IORedis({
  host: process.env.REDIS_HOST || "localhost",
  port: parseInt(process.env.REDIS_PORT || "6379"),
  maxRetriesPerRequest: null,
});
