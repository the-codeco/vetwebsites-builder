import { Editor } from '@tiptap/core';
import StarterKit from '@tiptap/starter-kit';
import Image from '@tiptap/extension-image';

// The admin's rich text editor: Tiptap (MIT) built on ProseMirror (MIT), bundled once into web/static/tiptap.js.
window.WBTiptap = { Editor: Editor, StarterKit: StarterKit, Image: Image };
