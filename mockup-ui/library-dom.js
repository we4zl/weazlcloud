// Compare source markup, not hydrated DOM: preview loaders add images, text and
// request state after rendering. Unchanged rows must keep those live nodes.
const sourceMarkup = new WeakMap();
const rowContainer = node => node.matches('.tree, .library-grid');
const key = node => node.hasAttribute('data-ctx-file') ? `file:${node.dataset.ctxFile}`
  : node.hasAttribute('data-ctx-folder') ? `folder:${node.dataset.ctxFolder}`
  : node.classList[0];

function remember(node) {
  sourceMarkup.set(node, node.outerHTML);
  if (rowContainer(node)) for (const row of node.children) remember(row);
}

function reconcile(parent, fresh) {
  const old = new Map([...parent.children].map(node => [key(node), node]));
  let cursor = parent.firstElementChild;
  for (const candidate of [...fresh.children]) {
    let node = old.get(key(candidate));
    old.delete(key(candidate));
    if (node && rowContainer(candidate)) {
      reconcile(node, candidate);
    } else if (!node || sourceMarkup.get(node) !== candidate.outerHTML) {
      remember(candidate);
      if (node) {
        if (cursor === node) cursor = candidate;
        node.replaceWith(candidate);
      }
      node = candidate;
    }
    if (node !== cursor) parent.insertBefore(node, cursor);
    cursor = node.nextElementSibling;
  }
  for (const node of old.values()) node.remove();
}

export function renderLibraryContent(content, html, scope) {
  const template = document.createElement('template');
  template.innerHTML = html;
  const fresh = template.content.firstElementChild;
  const workspace = content.querySelector(':scope > .library-workspace');
  if (!workspace || content.libraryScope !== scope) {
    for (const node of fresh.children) remember(node);
    content.replaceChildren(fresh);
    content.libraryScope = scope;
    return;
  }
  const anchor = [...workspace.querySelectorAll('[data-ctx-file], [data-ctx-folder]')]
    .find(node => node.getBoundingClientRect().bottom > 0);
  const top = anchor?.getBoundingClientRect().top;
  reconcile(workspace, fresh);
  if (anchor?.isConnected) window.scrollBy({top: anchor.getBoundingClientRect().top - top, behavior: 'instant'});
}
