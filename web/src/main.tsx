// SPDX-License-Identifier: AGPL-3.0-or-later
import '@mantine/core/styles.css';
import './styles.css';
import { createTheme, MantineProvider } from '@mantine/core';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router';
import { App } from './App';
import { pinAppRoot } from './routing';

export const theme = createTheme({
  primaryColor: 'teal',
  defaultRadius: 'sm',
  fontFamily: 'system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif',
  fontFamilyMonospace: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace',
});

const rootEl = document.getElementById('root');
if (!rootEl) throw new Error('Application root is missing.');

createRoot(rootEl).render(
  <StrictMode>
    <MantineProvider defaultColorScheme="auto" theme={theme}>
      <BrowserRouter basename={pinAppRoot()}>
        <App />
      </BrowserRouter>
    </MantineProvider>
  </StrictMode>,
);
