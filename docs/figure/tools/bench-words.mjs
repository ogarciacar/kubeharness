/**
 * The bench-part names in a text, with the POST before a path or a URL left
 * out. It uses nothing outside itself, so a journey can inline it into the page.
 */
export const benchWordsIn = (text) => text.replace(/\bPOST (?=\/|https?:\/\/)/g, "").match(/cartridge|diverter|gate valve|\bposts?\b|\btray\b/gi) ?? [];
