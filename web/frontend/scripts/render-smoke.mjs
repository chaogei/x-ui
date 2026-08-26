// 在 jsdom 里真正跑一遍打包产物，验证 Vue 应用挂得上、页面渲染得出内容。
//
// 为什么需要它：Go 那边的护栏只能证明 xui.js 在二进制里、体积不像占位符。
// 但产物只要有一处运行时异常（Vue 挂载抛错、boot 数据形状对不上、某个组件
// import 写错），面板照样返回 200，用户看到的是一张白页 —— 没有任何 Go 用例
// 能发现这件事，因为服务端从头到尾都是对的。
//
// 用法：页面 HTML 从 stdin 进，一行结果 JSON 从 stdout 出。
// 调用方见 web/e2e_render_test.go。

import fs from "node:fs";
import { fileURLToPath, URL } from "node:url";
import { JSDOM, VirtualConsole } from "jsdom";

const bundlePath = fileURLToPath(new URL("../../assets/dist/xui.js", import.meta.url));

// 产物是 ESM，而 jsdom 的 runScripts 只吃经典脚本。整份 bundle 的 export
// 只有末尾那一句，去掉即可当脚本执行；副作用（挂载 #app）不受影响。
const bundle = fs.readFileSync(bundlePath, "utf8").replace(/\bexport default /g, "void ");

const html = fs.readFileSync(0, "utf8");

// jsdom 没实现的浏览器 API 会以异常形式冒出来，但那是 jsdom 的边界，不是产物的问题。
// 这里按消息前缀白名单过滤，其余一律算失败。
const jsdomGaps = [
  "Not implemented: window.matchMedia",
  "matchMedia is not a function",
  "Not implemented: Window's getComputedStyle() method: with pseudo-elements",
  "Not implemented: window.scrollTo",
  "Not implemented: navigation",
];
const isJsdomGap = (msg) => jsdomGaps.some((gap) => msg.includes(gap));

const errors = [];
const record = (msg) => {
  if (!isJsdomGap(msg)) errors.push(msg);
};

const virtualConsole = new VirtualConsole();
virtualConsole.on("jsdomError", (e) => record("jsdomError: " + (e.stack || e.message)));
virtualConsole.on("error", (...args) => record("console.error: " + args.join(" ")));

const dom = new JSDOM(
  // <script type="module" src> 由 jsdom 忽略，产物在下面手动 eval，
  // 留着这个标签只会在控制台里多一条 404 噪音。
  html.replace(/<script type="module"[^>]*><\/script>/, ""),
  {
    runScripts: "dangerously",
    virtualConsole,
    url: "http://panel.test/",
    pretendToBeVisual: true,
  },
);

// antd 的响应式栅格会调 matchMedia。浏览器里不缺这个，jsdom 里得自己补。
dom.window.matchMedia = (query) => ({
  matches: false,
  media: query,
  onchange: null,
  addListener() {},
  removeListener() {},
  addEventListener() {},
  removeEventListener() {},
  dispatchEvent: () => false,
});

try {
  dom.window.eval(bundle);
} catch (e) {
  record("bundle threw while evaluating: " + (e.stack || e.message));
}

// Vue 的挂载与首轮渲染跨微任务，antd 组件还会再排一轮。
await new Promise((resolve) => setTimeout(resolve, 500));

const doc = dom.window.document;
const app = doc.querySelector("#app");

process.stdout.write(
  JSON.stringify({
    page: dom.window.__XUI__?.page ?? null,
    mounted: Boolean(app && app.children.length > 0),
    text: (app?.textContent ?? "").replace(/\s+/g, " ").trim(),
    inputs: doc.querySelectorAll("#app input").length,
    // 可交互控件不止 <button>：侧边栏是 role=menuitem，表格操作列是 <a>。
    // 只数 button 会把"页面其实是活的"误判成死页。
    actionable: doc.querySelectorAll("#app button, #app a, #app [role='menuitem']").length,
    errors,
  }) + "\n",
);

// 页面里可能留着 axios 轮询之类的定时器，不显式退出会一直挂着。
process.exit(0);
