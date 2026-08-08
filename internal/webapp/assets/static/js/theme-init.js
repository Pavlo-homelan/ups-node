(() => {
  'use strict';

  const root = document.documentElement;

  try {
    root.dataset.theme = localStorage.getItem('ups-pi-node-theme') || 'dark';
  } catch (error) {
    root.dataset.theme = 'dark';
  }

  try {
    root.dataset.widgetStyle = localStorage.getItem('ups-pi-node-widget-style') || 'neon';
  } catch (error) {
    root.dataset.widgetStyle = 'neon';
  }
})();
