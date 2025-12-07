import { Response } from 'express';

export const apiResponse = (res: Response, status: number, code: string, data?: any) => {
  return res.status(status).json({
    success: status >= 200 && status < 300,
    code: code,
    data: data || null,
    timestamp: new Date().toISOString()
  });
};
