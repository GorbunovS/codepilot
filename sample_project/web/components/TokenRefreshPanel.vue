<template>
  <div class="token_refresh">
    <h3>TokenRefresh</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// TokenRefreshPanel — панель «токен»: обновляет токен доступа.
export default {
  name: "TokenRefreshPanel",
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
    // refresh запускает обработку (токен).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.token_refresh { padding: 0.5rem; }
</style>
