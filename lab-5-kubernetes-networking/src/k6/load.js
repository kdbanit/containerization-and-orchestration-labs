import { sleep } from 'k6';
import http from 'k6/http';

export const options = {
  vus: 1,
  duration: '5m',
  noConnectionReuse: true,
  thresholds: {
    http_req_failed: ['rate==0'],
  },
};

export default function () {
  http.get(__ENV.TARGET_URL);
  sleep(0.3)
}
