import "./style.css";
import { Mylist, type MylistItem, type MylistPage } from "./mygo";

const $ = <T extends HTMLElement = HTMLElement>(selector: string) =>
  document.querySelector<T>(selector)!;

const form = $<HTMLFormElement>("#mylist-form");
const input = $<HTMLInputElement>("#mylist-input");
const submit = form.querySelector<HTMLButtonElement>("button[type=submit]")!;
const status = $("#mylist-status");
const result = $("#mylist-result");
const name = $("#mylist-name");
const meta = $("#mylist-meta");
const description = $("#mylist-description");
const items = $<HTMLOListElement>("#mylist-items");
const more = $<HTMLButtonElement>("#mylist-more");

// Lookup state lives in this window only; nothing is persisted.
let currentInput = "";
let nextPage = 1;
let busy = false;

function formatDuration(totalSeconds: number): string {
  const m = Math.floor(totalSeconds / 60);
  const s = totalSeconds % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}

function renderItem(item: MylistItem): HTMLLIElement {
  const li = document.createElement("li");
  if (item.available && item.title) {
    li.textContent = item.title;
    const sub = document.createElement("span");
    const parts = [item.uploader, item.duration ? formatDuration(item.duration) : ""].filter(Boolean);
    parts.unshift(item.watchId);
    sub.textContent = `（${parts.join(" / ")}）`;
    li.append(sub);
  } else {
    li.textContent = `取得できません（${item.watchId}）`;
  }
  return li;
}

function showPage(page: MylistPage, append: boolean): void {
  name.textContent = page.name;
  meta.textContent = page.owner
    ? `${page.owner}・全${page.totalItemCount}件`
    : `全${page.totalItemCount}件`;
  description.textContent = page.description;
  if (!append) {
    items.replaceChildren();
  }
  items.append(...page.items.map(renderItem));
  result.hidden = false;
  more.hidden = !page.hasNext;
  const shown = items.childElementCount;
  status.textContent =
    page.items.length === 0 && shown === 0
      ? "マイリストに項目がありません。"
      : `全${page.totalItemCount}件中${shown}件を表示しています。`;
}

async function load(append: boolean): Promise<void> {
  if (busy) return;
  busy = true;
  submit.disabled = true;
  input.disabled = true;
  more.disabled = true;
  status.textContent = "読み込み中…";
  try {
    const page = await Mylist.getPage(currentInput, nextPage);
    nextPage += 1;
    showPage(page, append);
  } catch (err) {
    status.textContent = err instanceof Error ? err.message : String(err);
  } finally {
    busy = false;
    submit.disabled = false;
    input.disabled = false;
    more.disabled = false;
  }
}

form.addEventListener("submit", (e) => {
  e.preventDefault();
  if (busy) return;
  currentInput = input.value;
  nextPage = 1;
  result.hidden = true;
  more.hidden = true;
  void load(false);
});

more.addEventListener("click", () => {
  void load(true);
});
