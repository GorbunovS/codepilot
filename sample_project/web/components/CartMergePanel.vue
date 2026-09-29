<template>
  <div class="cart_merge">
    <h3>CartMerge</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// CartMergePanel — панель «корзина»: сливает корзины после логина.
export default {
  name: "CartMergePanel",
  data() {
    return { processed: 0, skipped: 0 };
  },
  computed: {
    // status отображает итог обработки.
    status() {
      return `обработано: ${this.processed}, пропущено: ${this.skipped}`;
    },
  },
  methods: {
    // refresh запускает обработку (корзина).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.cart_merge { padding: 0.5rem; }
</style>
