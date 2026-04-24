axios.defaults.headers.post['Content-Type'] = 'application/x-www-form-urlencoded; charset=UTF-8';
axios.defaults.headers.common['X-Requested-With'] = 'XMLHttpRequest';

// 读取服务端渲染进 <head> 的 CSRF token，供非幂等请求自动附带。
function _readCsrfToken() {
    const meta = document.querySelector('meta[name="csrf-token"]');
    return meta ? meta.getAttribute('content') : '';
}

axios.interceptors.request.use(
    config => {
        config.data = Qs.stringify(config.data, {
            arrayFormat: 'repeat'
        });
        const method = (config.method || 'get').toLowerCase();
        if (method !== 'get' && method !== 'head' && method !== 'options') {
            const token = _readCsrfToken();
            if (token) {
                config.headers = config.headers || {};
                config.headers['X-CSRF-Token'] = token;
            }
        }
        return config;
    },
    error => Promise.reject(error)
);