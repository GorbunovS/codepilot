<template>
  <div class="price_rules">
    <h3>PriceRules</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// PriceRulesPanel — панель «цена»: пересчитывает цены по правилам.
export default {
  name: "PriceRulesPanel",
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
    // refresh запускает обработку (цена).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.price_rules { padding: 0.5rem; }
</style>
