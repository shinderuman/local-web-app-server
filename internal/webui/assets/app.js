const appsElement = document.querySelector("#apps");
const summaryElement = document.querySelector("#summary");

function render(apps) {
  summaryElement.textContent = `${apps.length}件のアプリを検出`;
  appsElement.replaceChildren();
  if (apps.length === 0) {
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = "インストール済みのアプリはありません。";
    appsElement.append(empty);
    return;
  }
  for (const app of apps) {
    const card = document.createElement("a");
    card.className = "app";
    card.href = app.url;

    const heading = document.createElement("h2");
    heading.textContent = app.name;
    const status = document.createElement("span");
    status.className = `status ${app.backend.state}`;
    status.textContent = app.backend.state;
    card.append(heading, status);

    if (app.backend.last_error) {
      const error = document.createElement("p");
      error.className = "error";
      error.textContent = app.backend.last_error;
      card.append(error);
    }
    appsElement.append(card);
  }
}

async function refresh() {
  try {
    const response = await fetch("/_local/apps", { cache: "no-store" });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    render(await response.json());
  } catch (error) {
    summaryElement.textContent = `状態を取得できません: ${error.message}`;
  }
}

refresh();
setInterval(refresh, 2000);
