'use strict';
const $ = (selector) => document.querySelector(selector);
const labels = {
  draft: 'Черновик',
  open: 'Ожидает оплаты',
  paid: 'Оплачен',
  expired: 'Срок истёк',
  cancelled: 'Отменён',
};
const events = {
  created: 'Создан черновик',
  updated: 'Обновлены условия',
  issued: 'Счёт выставлен',
  cancelled: 'Счёт отменён',
  expired: 'Срок оплаты истёк',
};
let offset = 0,
  selected = null,
  editing = null;
const limit = 20;
const date = (value) => new Date(value).toLocaleString('ru-RU');
function node(tag, text, className) {
  const el = document.createElement(tag);
  el.textContent = text;
  if (className) el.className = className;
  return el;
}
async function api(path, method = 'GET', body) {
  const res = await fetch('/api' + path, {
    method,
    headers: {
      ...(body ? { 'Content-Type': 'application/json' } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (res.status === 204) return null;
  const data = await res.json();
  if (!res.ok) throw new Error(data.error || 'Ошибка запроса');
  return data;
}
function notice(message) {
  $('#notice').textContent = message;
}
async function load() {
  const items = await api(
    '/invoices?limit=' + limit + '&offset=' + offset + '&status=' + $('#filter').value,
  );
  $('#invoices').replaceChildren();
  $('#empty').hidden = items.length > 0;
  items.forEach((i) => {
    const tr = document.createElement('tr');
    const title = document.createElement('td');
    const link = node('button', i.description, 'invoice-link');
    link.onclick = () => show(i.id).catch((e) => notice(e.message));
    title.append(link, node('small', i.id));
    tr.append(title, node('td', i.amount));
    const status = document.createElement('td');
    status.append(node('span', labels[i.status], 'badge ' + i.status));
    tr.append(status, node('td', date(i.expires_at)));
    $('#invoices').append(tr);
  });
  $('#prev').disabled = offset === 0;
  $('#next').disabled = items.length < limit;
  $('#page').textContent = 'Страница ' + (offset / limit + 1);
}
$('#refresh').onclick = () => load().catch((e) => notice(e.message));
$('#filter').onchange = () => {
  offset = 0;
  load().catch((e) => notice(e.message));
};
$('#prev').onclick = () => {
  offset = Math.max(0, offset - limit);
  load().catch((e) => notice(e.message));
};
$('#next').onclick = () => {
  offset += limit;
  load().catch((e) => notice(e.message));
};
document
  .querySelectorAll('[data-close]')
  .forEach((el) => (el.onclick = () => document.getElementById(el.dataset.close).close()));
function edit(i = null) {
  editing = i;
  const form = $('#invoice-form');
  form.reset();
  $('#form-error').textContent = '';
  $('#editor-title').textContent = i ? 'Редактировать черновик' : 'Новый счёт';
  if (i)
    for (const name of ['description', 'amount', 'chain_id', 'token_address', 'recipient_address'])
      form.elements[name].value = i[name];
  const d = i ? new Date(i.expires_at) : new Date(Date.now() + 86400000);
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
  form.elements.expires_at.value = d.toISOString().slice(0, 16);
  $('#details').close();
  $('#editor').showModal();
}
$('#new').onclick = () => edit();
$('#invoice-form').onsubmit = async (e) => {
  e.preventDefault();
  $('#save').disabled = true;
  $('#form-error').textContent = '';
  try {
    const values = Object.fromEntries(new FormData(e.target));
    values.chain_id = Number(values.chain_id);
    values.expires_at = new Date(values.expires_at).toISOString();
    if (editing) values.version = editing.version;
    const i = await api(
      editing ? '/invoices/' + editing.id : '/invoices',
      editing ? 'PUT' : 'POST',
      values,
    );
    $('#editor').close();
    offset = 0;
    await load();
    await show(i.id);
  } catch (err) {
    $('#form-error').textContent = err.message;
  } finally {
    $('#save').disabled = false;
  }
};
async function show(id) {
  const [i, history] = await Promise.all([
    api('/invoices/' + id),
    api('/invoices/' + id + '/events'),
  ]);
  selected = i;
  const dl = document.createElement('dl');
  for (const [key, value] of [
    ['Назначение', i.description],
    ['Статус', labels[i.status]],
    ['Сумма', i.amount + ' минимальных единиц'],
    ['Chain ID', i.chain_id],
    ['Токен', i.token_address],
    ['Получатель', i.recipient_address],
    ['Срок оплаты', date(i.expires_at)],
    ['Версия', i.version],
  ])
    dl.append(node('dt', key), node('dd', value));
  $('#detail-content').replaceChildren(dl);
  $('#detail-error').textContent = '';
  $('#actions').replaceChildren();
  function button(text, callback, css) {
    const b = node('button', text, css);
    b.onclick = callback;
    $('#actions').append(b);
  }
  if (i.status === 'draft') {
    button('Выставить', () => change('issue'));
    button('Изменить', () => edit(i), 'secondary');
    button('Удалить', () => remove(), 'danger');
  }
  if (i.status === 'open') button('Отменить счёт', () => change('cancel'), 'danger');
  $('#history').replaceChildren(
    ...history.map((e) => node('li', date(e.occurred_at) + ' · ' + (events[e.type] || e.type))),
  );
  if (!$('#details').open) $('#details').showModal();
}
async function change(action) {
  if (
    !confirm(
      action === 'issue'
        ? 'Выставить счёт? После этого условия нельзя изменить.'
        : 'Отменить счёт?',
    )
  )
    return;
  $('#actions')
    .querySelectorAll('button')
    .forEach((b) => (b.disabled = true));
  try {
    await api('/invoices/' + selected.id + '/' + action, 'POST', { version: selected.version });
    await load();
    await show(selected.id);
  } catch (e) {
    $('#detail-error').textContent = e.message;
    $('#actions')
      .querySelectorAll('button')
      .forEach((b) => (b.disabled = false));
  }
}
async function remove() {
  if (!confirm('Удалить этот черновик?')) return;
  try {
    await api('/invoices/' + selected.id + '?version=' + selected.version, 'DELETE');
    $('#details').close();
    await load();
  } catch (e) {
    $('#detail-error').textContent = e.message;
  }
}

function fillExample(expiring) {
  const form = $('#invoice-form');
  const values = {
    description: expiring ? 'Пример: проверка истечения срока' : 'Пример: разработка сайта',
    amount: '10000000',
    chain_id: '84532',
    token_address: '0x1111111111111111111111111111111111111111',
    recipient_address: '0x2222222222222222222222222222222222222222',
  };
  for (const [name, value] of Object.entries(values)) form.elements[name].value = value;
  const expiry = new Date(Date.now() + (expiring ? 120000 : 86400000));
  expiry.setMinutes(expiry.getMinutes() - expiry.getTimezoneOffset());
  form.elements.expires_at.value = expiry.toISOString().slice(0, 19);
  $('#form-error').textContent = '';
}
$('#example-standard').onclick = () => fillExample(false);
$('#example-expiring').onclick = () => fillExample(true);
load().catch((e) => notice(e.message));
