import {CaptionView} from './view.js';
const $ = (id) => document.getElementById(id);
const view = new CaptionView();
function render() {$('japanese').textContent = view.japanese || '等待日文原文…'; $('chinese').textContent = view.chinese || '等待中文译文…'; $('status').textContent = view.status;}
function settings(value) {
  document.body.classList.toggle('chinese-only', value.mode === 'chinese');
  $('mode').textContent = value.mode === 'chinese' ? '双语显示' : '仅中文'; $('mode').setAttribute('aria-pressed', String(value.mode === 'chinese'));
  document.documentElement.style.setProperty('--font-size', `${value.fontSize}px`); document.documentElement.style.setProperty('--background-alpha', String(value.opacity));
  $('font').value = value.fontSize; $('fontValue').textContent = value.fontSize;
  $('opacity').value = Math.round(value.opacity * 100); $('opacityValue').textContent = `${Math.round(value.opacity * 100)}%`;
}
let current;
const initial = await window.captions.init(); current = initial.settings; settings(current); view.apply(initial.state); render();
window.captions.onState((event) => {if (view.apply(event)) render();});
async function change(patch) {current = {...current, ...patch}; settings(current); await window.captions.setSettings(current);}
$('mode').addEventListener('click', () => change({mode: current.mode === 'chinese' ? 'bilingual' : 'chinese'}));
$('font').addEventListener('input', () => change({fontSize: Number($('font').value)}));
$('opacity').addEventListener('input', () => change({opacity: Number($('opacity').value) / 100}));
$('open').addEventListener('click', () => window.captions.openCapture());
$('close').addEventListener('click', () => window.captions.close());
