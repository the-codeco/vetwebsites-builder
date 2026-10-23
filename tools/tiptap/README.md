# The admin's rich text editor

`web/static/tiptap.js` is [Tiptap](https://tiptap.dev) (MIT) with its ProseMirror core (MIT), bundled once into a
single file, so the admin keeps having no JavaScript build step. To change the editor's version or extensions, edit
`entry.js` or `package.json` here, then:

```bash
cd tools/tiptap
npm ci --ignore-scripts
npx esbuild entry.js --bundle --minify --format=iife --target=es2019 --legal-comments=none --outfile=../../web/static/tiptap.js
```

`web/static/admin.js` (the "rich text editor" section) turns every `textarea[data-richtext]` into this editor and keeps
the textarea up to date with the editor's HTML, so forms, the live preview and the server's cleaning work as before.
